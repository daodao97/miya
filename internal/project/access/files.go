package access

import (
	"context"
	"io"

	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
)

type Files interface {
	ReadDir(root, rel string) ([]projectfiles.FileEntry, error)
	ReadImage(root, rel string) ([]byte, error)
	ReadText(root, rel string) (string, error)
	Diff(ctx context.Context, root string, src projectgit.DiffSource) (projectgit.DiffSnapshot, error)
	Sides(ctx context.Context, root string, snap projectgit.DiffSnapshot, f projectgit.ChangedFile) (oldText, newText string, err error)
	// Terminal connects a terminal in dir; nil runs a local shell.
	Terminal(dir string) (io.ReadWriteCloser, error)
}
