package appmodel

import (
	"context"
	"io"

	"miya/internal/worktree"
)

type WorkspaceFiles interface {
	ReadDir(root, rel string) ([]worktree.FileEntry, error)
	ReadImage(root, rel string) ([]byte, error)
	ReadText(root, rel string) (string, error)
	Diff(ctx context.Context, root string, src worktree.DiffSource) (worktree.DiffSnapshot, error)
	Sides(ctx context.Context, root string, snap worktree.DiffSnapshot, f worktree.ChangedFile) (oldText, newText string, err error)
	// Terminal connects a terminal in dir; nil runs a local shell.
	Terminal(dir string) (io.ReadWriteCloser, error)
}
