package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/agentusage"
	"miya/internal/fsutil"
	"miya/internal/thumbnail"
	"miya/internal/worktree"
)

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
					out = append(out, hostEntry{Name: e.Name(), Path: filepath.Join(root, e.Name()), Dir: true})
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
			return hostSides{Old: old, New: new}, nil
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
				out = append(out, hostEntry{Name: e.Name, Path: e.Rel, Dir: e.Dir})
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
