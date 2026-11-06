package controller

import (
	context "context"
	io "io"
	appmodel "miya/internal/appmodel"
	worktree "miya/internal/worktree"
	filepath "path/filepath"
)

// workspaceFiles reads a project's files and changes and opens its
// terminals: on this Mac, or on the server for a remote server workspace.
// Its methods may run off the controller's thread.
type workspaceFiles = appmodel.WorkspaceFiles

// workspaceFiles is the open workspace's files. Call it on the controller's
// thread, as the workspace it picks can change there.
func (c *Controller) WorkspaceFiles() workspaceFiles {
	if c.RemoteHost() {
		return serverFiles{c.Remote}
	}
	return localFiles{}
}

// localFiles are a workspace on this Mac.
type localFiles struct{}

func (localFiles) ReadDir(root, rel string) ([]worktree.FileEntry, error) {
	return worktree.ReadDirEntries(root, rel)
}

func (localFiles) ReadImage(root, rel string) ([]byte, error) {
	return worktree.ReadImageFile(filepath.Join(root, rel))
}

func (localFiles) ReadText(root, rel string) (string, error) {
	return worktree.ReadTextFile(filepath.Join(root, rel))
}

func (localFiles) Diff(ctx context.Context, root string, src worktree.DiffSource) (worktree.DiffSnapshot, error) {
	return worktree.CollectDiff(ctx, root, src)
}

func (localFiles) Sides(ctx context.Context, root string, snap worktree.DiffSnapshot, f worktree.ChangedFile) (string, string, error) {
	oldText, newText := worktree.FileSides(ctx, root, snap, f)
	return oldText, newText, nil
}

func (localFiles) Terminal(string) (io.ReadWriteCloser, error) { return nil, nil }

// serverFiles are a remote server workspace, reached through the server's
// "host" requests. Each call has the service client's own timeout.
type serverFiles struct{ cl *appmodel.ServiceClient }

func (s serverFiles) ReadDir(root, rel string) ([]worktree.FileEntry, error) {
	var rows []appmodel.HostEntry
	err := s.cl.Call("host", appmodel.HostRequest{Action: "dir", Project: root, Path: rel}, &rows)
	var entries []worktree.FileEntry
	for _, e := range rows {
		entries = append(entries, worktree.FileEntry{Name: e.Name, Rel: e.Path, Dir: e.Dir})
	}
	return entries, err
}

func (s serverFiles) ReadImage(root, rel string) ([]byte, error) {
	var data []byte
	err := s.cl.Call("host", appmodel.HostRequest{Action: "image", Project: root, Path: rel}, &data)
	return data, err
}

func (s serverFiles) ReadText(root, rel string) (string, error) {
	var text string
	err := s.cl.Call("host", appmodel.HostRequest{Action: "read", Project: root, Path: rel}, &text)
	return text, err
}

func (s serverFiles) Diff(_ context.Context, root string, src worktree.DiffSource) (worktree.DiffSnapshot, error) {
	var snap worktree.DiffSnapshot
	err := s.cl.Call("host", appmodel.HostRequest{Action: "diff", Project: root, Source: src}, &snap)
	return snap, err
}

func (s serverFiles) Sides(_ context.Context, root string, snap worktree.DiffSnapshot, f worktree.ChangedFile) (string, string, error) {
	var sides appmodel.HostSides
	err := s.cl.Call("host", appmodel.HostRequest{Action: "sides", Project: root, Snapshot: snap, File: f}, &sides)
	return sides.Old, sides.New, err
}

func (s serverFiles) Terminal(dir string) (io.ReadWriteCloser, error) {
	return appmodel.OpenRemoteTerminal(s.cl, dir)
}
