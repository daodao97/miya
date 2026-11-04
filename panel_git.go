package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// goAsync runs work that touches the disk or git off the UI thread. Tests
// replace it to run the work in place.
var goAsync = func(f func()) { go f() }

// emptyTree is git's tree of no files, the base of a repository without commits.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// diffSource is what the review compares, as Waku's review sources: the
// working tree, the index, or the branch since it left the default branch.
type diffSource int

const (
	sourceUncommitted diffSource = iota // HEAD → working tree
	sourceUnstaged                      // index → working tree
	sourceStaged                        // HEAD → index
	sourceBranch                        // merge base with the default branch → working tree
)

var diffSourceNames = []string{tr("Uncommitted changes"), tr("Unstaged changes"), tr("Staged changes"), tr("Against default branch")}

type changedFile struct {
	Path           string
	Added, Deleted int
	Binary         bool
	Untracked      bool
}

type diffSnapshot struct {
	Source diffSource
	// Base is the old side's commit or tree; empty means the index.
	Base           string
	Files          []changedFile
	Added, Deleted int
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = dir
	// Reading must never take the index lock from a concurrent agent.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

func gitHead(ctx context.Context, cwd string) string {
	if out, err := git(ctx, cwd, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		if head := strings.TrimSpace(out); head != "" {
			return head
		}
	}
	return emptyTree
}

// branchBase is where the current branch left the default branch, or HEAD
// on the default branch itself.
func branchBase(ctx context.Context, cwd, head string) string {
	if head == emptyTree {
		return head
	}
	current := ""
	if out, err := git(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		current = strings.TrimSpace(out)
	}
	candidates := []string{}
	if out, err := git(ctx, cwd, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		candidates = append(candidates, strings.TrimPrefix(strings.TrimSpace(out), "origin/"))
	}
	candidates = append(candidates, "main", "master")
	for _, branch := range candidates {
		if branch == "" || branch == current {
			continue
		}
		if _, err := git(ctx, cwd, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
			continue
		}
		if out, err := git(ctx, cwd, "merge-base", "HEAD", branch); err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
	}
	return head
}

// collectDiff lists the files a source changes, relative to cwd, with the
// lines each adds and deletes.
func collectDiff(ctx context.Context, cwd string, src diffSource) (diffSnapshot, error) {
	if _, err := git(ctx, cwd, "rev-parse", "--show-toplevel"); err != nil {
		return diffSnapshot{}, errors.New(tr("This workspace is not a Git repository"))
	}
	head := gitHead(ctx, cwd)
	snap := diffSnapshot{Source: src}
	args := []string{"diff", "--no-ext-diff", "--no-color", "--numstat", "--relative", "--no-renames"}
	untracked := true
	switch src {
	case sourceUncommitted:
		snap.Base = head
		args = append(args, head)
	case sourceUnstaged:
		// The index against the working tree.
	case sourceStaged:
		snap.Base = head
		args = append(args, "--cached", head)
		untracked = false
	case sourceBranch:
		snap.Base = branchBase(ctx, cwd, head)
		args = append(args, snap.Base)
	}
	out, err := git(ctx, cwd, append(args, "--", ".")...)
	if err != nil {
		return diffSnapshot{}, err
	}
	for _, row := range strings.Split(out, "\n") {
		fields := strings.SplitN(row, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		f := changedFile{Path: fields[2]}
		if fields[0] == "-" || fields[1] == "-" {
			f.Binary = true
		} else {
			f.Added, _ = strconv.Atoi(fields[0])
			f.Deleted, _ = strconv.Atoi(fields[1])
		}
		snap.Files = append(snap.Files, f)
	}
	if untracked {
		if out, err := git(ctx, cwd, "ls-files", "--others", "--exclude-standard", "--", "."); err == nil {
			for _, path := range strings.Split(out, "\n") {
				if path == "" {
					continue
				}
				f := changedFile{Path: path, Untracked: true}
				if data, err := os.ReadFile(filepath.Join(cwd, path)); err == nil {
					if isBinary(data) {
						f.Binary = true
					} else {
						f.Added = bytes.Count(data, []byte{'\n'})
						if len(data) > 0 && data[len(data)-1] != '\n' {
							f.Added++
						}
					}
				}
				snap.Files = append(snap.Files, f)
			}
		}
	}
	sort.Slice(snap.Files, func(i, j int) bool { return snap.Files[i].Path < snap.Files[j].Path })
	for _, f := range snap.Files {
		snap.Added += f.Added
		snap.Deleted += f.Deleted
	}
	return snap, nil
}

// showFile reads a file at a commit, or in the index when ref is empty. A
// file absent on that side reads as empty, as an added or deleted one is.
func showFile(ctx context.Context, cwd, ref, path string) string {
	if ref == emptyTree {
		return ""
	}
	out, err := git(ctx, cwd, "show", ref+":./"+path)
	if err != nil {
		return ""
	}
	return out
}

func worktreeFile(cwd, path string) string {
	data, err := os.ReadFile(filepath.Join(cwd, path))
	if err != nil {
		return ""
	}
	return string(data)
}

// fileSides returns the old and new text of a changed file.
func fileSides(ctx context.Context, cwd string, snap diffSnapshot, f changedFile) (string, string) {
	var old, new string
	switch snap.Source {
	case sourceStaged:
		old, new = showFile(ctx, cwd, snap.Base, f.Path), showFile(ctx, cwd, "", f.Path)
	case sourceUnstaged:
		old, new = showFile(ctx, cwd, "", f.Path), worktreeFile(cwd, f.Path)
	default:
		old, new = showFile(ctx, cwd, snap.Base, f.Path), worktreeFile(cwd, f.Path)
	}
	if f.Untracked {
		old = ""
	}
	return old, new
}

type fileEntry struct {
	name, rel string
	dir       bool
}

// readDirEntries lists a directory of the workspace, folders first.
func readDirEntries(root, rel string) ([]fileEntry, error) {
	entries, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	out := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		dir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(root, rel, name)); err == nil {
				dir = info.IsDir()
			}
		}
		out = append(out, fileEntry{name: name, rel: filepath.ToSlash(filepath.Join(rel, name)), dir: dir})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out, nil
}

const maxEditableFile = 4 << 20

func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

// readTextFile reads a file the editor can show.
func readTextFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxEditableFile {
		return "", fmt.Errorf(tr("File too large (%.1f MB) to open here"), float64(info.Size())/(1<<20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if isBinary(data) {
		return "", errors.New(tr("Cannot display binary files"))
	}
	return string(data), nil
}

const maxPreviewImage = 16 << 20

// imageFileKind says how the panel shows a picture: "svg" for an SVG,
// which is text too and can be edited, "bitmap" for the formats
// ui.DecodeBitmap reads, and "" for any other file.
func imageFileKind(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".svg":
		return "svg"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return "bitmap"
	}
	return ""
}

// readImageFile reads a bitmap for the panel to preview.
func readImageFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxPreviewImage {
		return nil, fmt.Errorf(tr("Image too large (%.1f MB) to preview here"), float64(info.Size())/(1<<20))
	}
	return os.ReadFile(path)
}

// writeFileAtomic replaces a file without leaving it half written, keeping
// its permissions.
func writeFileAtomic(path string, write func(f *os.File) error) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// browserURL turns what is typed in the address bar into a URL: a port is a
// local server, a host without a scheme is https, local hosts http.
func browserURL(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "http://localhost:" + s
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "about:") {
		return s
	}
	for _, local := range []string{"localhost", "127.", "0.0.0.0", "[::1]"} {
		if strings.HasPrefix(s, local) {
			return "http://" + s
		}
	}
	return "https://" + s
}
