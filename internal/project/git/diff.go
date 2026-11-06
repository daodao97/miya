// Package worktree reads a workspace for the review panel: Git diffs
// between review sources and the text or images of its files.
package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	projectfiles "miya/internal/project/files"
	"miya/locales"
)

// emptyTree is git's tree of no files, the base of a repository without commits.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// DiffSource is what the review compares, as Waku's review sources: the
// working tree, the index, or the branch since it left the default branch.
type DiffSource int

const (
	SourceUncommitted DiffSource = iota // HEAD → working tree
	SourceUnstaged                      // index → working tree
	SourceStaged                        // HEAD → index
	SourceBranch                        // merge base with the default branch → working tree
)

// DiffSourceNames are the sources' names in DiffSource order, in the current
// interface language.
func DiffSourceNames() []string {
	return []string{tr("Uncommitted changes"), tr("Unstaged changes"), tr("Staged changes"), tr("Against default branch")}
}

type ChangedFile struct {
	Path           string
	Added, Deleted int
	Binary         bool
	Untracked      bool
}

type DiffSnapshot struct {
	Source DiffSource
	// Base is the old side's commit or tree; empty means the index.
	Base           string
	Files          []ChangedFile
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

// CollectDiff lists the files a source changes, relative to cwd, with the
// lines each adds and deletes.
func CollectDiff(ctx context.Context, cwd string, src DiffSource) (DiffSnapshot, error) {
	if _, err := git(ctx, cwd, "rev-parse", "--show-toplevel"); err != nil {
		return DiffSnapshot{}, errors.New(tr("This workspace is not a Git repository"))
	}
	head := gitHead(ctx, cwd)
	snap := DiffSnapshot{Source: src}
	args := []string{"diff", "--no-ext-diff", "--no-color", "--numstat", "--relative", "--no-renames"}
	untracked := true
	switch src {
	case SourceUncommitted:
		snap.Base = head
		args = append(args, head)
	case SourceUnstaged:
		// The index against the working tree.
	case SourceStaged:
		snap.Base = head
		args = append(args, "--cached", head)
		untracked = false
	case SourceBranch:
		snap.Base = branchBase(ctx, cwd, head)
		args = append(args, snap.Base)
	}
	out, err := git(ctx, cwd, append(args, "--", ".")...)
	if err != nil {
		return DiffSnapshot{}, err
	}
	for _, row := range strings.Split(out, "\n") {
		fields := strings.SplitN(row, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		f := ChangedFile{Path: fields[2]}
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
				f := ChangedFile{Path: path, Untracked: true}
				if data, err := os.ReadFile(filepath.Join(cwd, path)); err == nil {
					if projectfiles.IsBinary(data) {
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

// FileSides returns the old and new text of a changed file.
func FileSides(ctx context.Context, cwd string, snap DiffSnapshot, f ChangedFile) (string, string) {
	var old, new string
	switch snap.Source {
	case SourceStaged:
		old, new = showFile(ctx, cwd, snap.Base, f.Path), showFile(ctx, cwd, "", f.Path)
	case SourceUnstaged:
		old, new = showFile(ctx, cwd, "", f.Path), worktreeFile(cwd, f.Path)
	default:
		old, new = showFile(ctx, cwd, snap.Base, f.Path), worktreeFile(cwd, f.Path)
	}
	if f.Untracked {
		old = ""
	}
	return old, new
}

func tr(s string) string { return locales.T(s) }
