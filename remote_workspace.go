package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/agentusage"
	"miya/internal/appdir"
	"miya/internal/fsutil"
	"miya/internal/remote"
	"miya/internal/thumbnail"
	"miya/internal/worktree"
)

type hostRequest struct {
	Action, Project, Path, Text string
	Source                      worktree.DiffSource
	Snapshot                    worktree.DiffSnapshot
	File                        worktree.ChangedFile
	Images                      []remote.Image
	Reset                       *agentusage.ResetAttempt
}

type hostEntry struct {
	Name, Path string
	Dir        bool
}
type hostSides struct{ Old, New string }
type hostCommands struct {
	Choices []composerChoice
	Known   bool
}

func (c *chatController) prepareHost(r hostRequest) (deferred, error) {
	// Desktop clients have the same authority as a local window. Project
	// operations still bind their root, so an old tab cannot silently follow
	// a different client's selected project.
	if r.Project != "" {
		known := r.Project == c.cwd
		for _, p := range c.projects() {
			known = known || p.Path == r.Project
		}
		if !known {
			return nil, errors.New("Project is not in the server's project list")
		}
	}
	return func() (any, error) {
		ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
		defer cancel()
		switch r.Action {
		case "browse":
			root := r.Path
			if root == "" {
				root, _ = os.UserHomeDir()
			}
			if !filepath.IsAbs(root) {
				return nil, errors.New("Enter an absolute path on the server")
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				return nil, err
			}
			out := []hostEntry{}
			for _, e := range entries {
				if e.IsDir() {
					out = append(out, hostEntry{e.Name(), filepath.Join(root, e.Name()), true})
				}
			}
			return out, nil
		case "upload":
			return savePhoneImages(r.Images)
		case "upload-files":
			return saveDesktopUploads(r.Images)
		case "preview":
			return thumbnail.File(r.Path)
		case "usage":
			return []agentusage.Reading{agentusage.Read(ctx, "codex"), agentusage.Read(ctx, "claude")}, nil
		case "usage-reset":
			if r.Reset == nil {
				return nil, errors.New("Confirmed reset operation missing")
			}
			return agentusage.UseCodexReset(ctx, *r.Reset)
		}
		if r.Project == "" {
			return nil, errors.New("Server project not specified")
		}
		switch r.Action {
		case "index":
			return indexProjectFiles(ctx, r.Project)
		case "diff":
			if r.Source < worktree.SourceUncommitted || r.Source > worktree.SourceBranch {
				return nil, errors.New("Invalid diff source")
			}
			return worktree.CollectDiff(ctx, r.Project, r.Source)
		case "sides":
			if _, err := hostProjectPath(r.Project, r.File.Path); err != nil {
				return nil, err
			}
			old, new := worktree.FileSides(ctx, r.Project, r.Snapshot, r.File)
			if len(old)+len(new) > 2*worktree.MaxEditableFile {
				return nil, errors.New("Diff is too large")
			}
			return hostSides{old, new}, nil
		}
		path, err := hostProjectPath(r.Project, r.Path)
		if err != nil {
			return nil, err
		}
		switch r.Action {
		case "dir":
			entries, err := worktree.ReadDirEntries(r.Project, r.Path)
			out := make([]hostEntry, 0, len(entries))
			for _, e := range entries {
				out = append(out, hostEntry{e.Name, e.Rel, e.Dir})
			}
			return out, err
		case "read":
			return worktree.ReadTextFile(path)
		case "image":
			if worktree.ImageFileKind(r.Path) != "bitmap" {
				return nil, errors.New("Not a previewable image")
			}
			return worktree.ReadImageFile(path)
		case "write":
			if len(r.Text) > worktree.MaxEditableFile || worktree.IsBinary([]byte(r.Text)) {
				return nil, errors.New("Unable to save this file")
			}
			return nil, fsutil.WriteFileAtomic(path, func(f *os.File) error { _, err := io.WriteString(f, r.Text); return err })
		}
		return nil, errors.New("Unknown server operation")
	}, nil
}

func hostProjectPath(root, rel string) (string, error) {
	if rel == "" {
		return root, nil
	}
	if !filepath.IsLocal(rel) {
		return "", errors.New("Invalid project path")
	}
	path := filepath.Join(root, rel)
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		var parent string
		parent, err = filepath.EvalSymlinks(filepath.Dir(path))
		if err == nil {
			resolved = filepath.Join(parent, filepath.Base(path))
		}
	}
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(base, resolved)
	if err != nil || !filepath.IsLocal(inside) {
		return "", errors.New("File is outside the project directory")
	}
	return path, nil
}

func (c *chatController) hostCall(r hostRequest, out any) error {
	if !c.remoteHost() {
		return errors.New("Server disconnected")
	}
	return c.remote.Call("host", r, out)
}

// Read local attachments before crossing the machine boundary. Server-side
// retry/draft paths are already server paths and must not be re-uploaded.
func (c *chatController) uploadRemoteAttachments(paths []string) ([]string, error) {
	result := append([]string(nil), paths...)
	var local []string
	var positions []int
	for i, path := range paths {
		if strings.HasPrefix(path, "miya-server:") {
			result[i] = strings.TrimPrefix(path, "miya-server:")
			continue
		}
		local, positions = append(local, path), append(positions, i)
	}
	images, err := readDesktopUploads(local)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return result, nil
	}
	var uploaded []string
	if err := c.hostCall(hostRequest{Action: "upload-files", Images: images}, &uploaded); err != nil {
		return nil, err
	}
	if len(uploaded) != len(positions) {
		return nil, errors.New("Incomplete server upload result")
	}
	for i, pos := range positions {
		result[pos] = uploaded[i]
	}
	return result, nil
}

func checkDesktopUploads(count, size, total int) error {
	if count > maxPhoneImages || size > maxPhoneImageBytes || total > maxPhoneImageTotal {
		return fmt.Errorf("Send up to %d attachments at a time, %d MB each, %d MB total", maxPhoneImages, maxPhoneImageBytes>>20, maxPhoneImageTotal>>20)
	}
	return nil
}

func readDesktopUploads(paths []string) ([]remote.Image, error) {
	if err := checkDesktopUploads(len(paths), 0, 0); err != nil {
		return nil, err
	}
	var out []remote.Image
	total := 0
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxPhoneImageBytes+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		total += len(data)
		if err := checkDesktopUploads(len(paths), len(data), total); err != nil {
			return nil, err
		}
		out = append(out, remote.Image{Name: filepath.Base(path), Data: data})
	}
	return out, nil
}

func saveDesktopUploads(files []remote.Image) (paths []string, err error) {
	if err := checkDesktopUploads(len(files), 0, 0); err != nil {
		return nil, err
	}
	dir := filepath.Join(appdir.Dir(), "uploads")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	pruneOldUploads(dir)
	defer func() {
		if err != nil {
			for _, path := range paths {
				os.Remove(path)
			}
			paths = nil
		}
	}()
	total := 0
	for _, file := range files {
		total += len(file.Data)
		if err := checkDesktopUploads(len(files), len(file.Data), total); err != nil {
			return paths, err
		}
		name := filepath.Base(file.Name)
		if strings.ContainsAny(name, "\\\x00") || name == "." || name == ".." {
			return paths, errors.New("Invalid attachment name")
		}
		f, err := os.CreateTemp(dir, "desktop-*-"+name)
		if err != nil {
			return paths, err
		}
		paths = append(paths, f.Name())
		_, err = f.Write(file.Data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return paths, err
		}
	}
	return paths, nil
}
