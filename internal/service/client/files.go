package client

import (
	"context"
	"io"

	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
	svcproto "miya/internal/service/protocol"
)

// serverFiles are a remote server workspace, reached through the server's
// "host" requests. Each call has the service client's own timeout.
type Files struct{ Client *Client }

func (s Files) ReadDir(root, rel string) ([]projectfiles.FileEntry, error) {
	var rows []svcproto.HostEntry
	err := s.Client.Call("host", svcproto.HostRequest{Action: "dir", Project: root, Path: rel}, &rows)
	var entries []projectfiles.FileEntry
	for _, e := range rows {
		entries = append(entries, projectfiles.FileEntry{Name: e.Name, Rel: e.Path, Dir: e.Dir})
	}
	return entries, err
}

func (s Files) ReadImage(root, rel string) ([]byte, error) {
	var data []byte
	err := s.Client.Call("host", svcproto.HostRequest{Action: "image", Project: root, Path: rel}, &data)
	return data, err
}

func (s Files) ReadText(root, rel string) (string, error) {
	var text string
	err := s.Client.Call("host", svcproto.HostRequest{Action: "read", Project: root, Path: rel}, &text)
	return text, err
}

func (s Files) Diff(_ context.Context, root string, src projectgit.DiffSource) (projectgit.DiffSnapshot, error) {
	var snap projectgit.DiffSnapshot
	err := s.Client.Call("host", svcproto.HostRequest{Action: "diff", Project: root, Source: src}, &snap)
	return snap, err
}

func (s Files) Sides(_ context.Context, root string, snap projectgit.DiffSnapshot, f projectgit.ChangedFile) (string, string, error) {
	var sides svcproto.HostSides
	err := s.Client.Call("host", svcproto.HostRequest{Action: "sides", Project: root, Snapshot: snap, File: f}, &sides)
	return sides.Old, sides.New, err
}

func (s Files) Terminal(dir string) (io.ReadWriteCloser, error) {
	return OpenTerminal(s.Client, dir)
}
