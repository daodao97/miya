package access

import (
	"context"
	"io"
	"path/filepath"

	projectfiles "miya/internal/project/files"
	projectgit "miya/internal/project/git"
)

// localFiles are a workspace on this Mac.
type Local struct{}

func (Local) ReadDir(root, rel string) ([]projectfiles.FileEntry, error) {
	return projectfiles.ReadDirEntries(root, rel)
}

func (Local) ReadImage(root, rel string) ([]byte, error) {
	return projectfiles.ReadImageFile(filepath.Join(root, rel))
}

func (Local) ReadText(root, rel string) (string, error) {
	return projectfiles.ReadTextFile(filepath.Join(root, rel))
}

func (Local) Diff(ctx context.Context, root string, src projectgit.DiffSource) (projectgit.DiffSnapshot, error) {
	return projectgit.CollectDiff(ctx, root, src)
}

func (Local) Sides(ctx context.Context, root string, snap projectgit.DiffSnapshot, f projectgit.ChangedFile) (string, string, error) {
	oldText, newText := projectgit.FileSides(ctx, root, snap, f)
	return oldText, newText, nil
}

func (Local) Terminal(string) (io.ReadWriteCloser, error) { return nil, nil }
