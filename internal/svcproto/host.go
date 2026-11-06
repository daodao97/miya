package svcproto

import (
	"errors"
	"os"
	"path/filepath"

	"miya/internal/agentusage"
	"miya/internal/model"
	"miya/internal/remote"
	"miya/internal/worktree"
)

type HostCommands struct {
	Choices []model.ComposerChoice
	Known   bool
}

type HostEntry struct {
	Name, Path string
	Dir        bool
}

func HostProjectPath(root, rel string) (string, error) {
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

type HostRequest struct {
	Action, Project, Path, Text string
	Source                      worktree.DiffSource
	Snapshot                    worktree.DiffSnapshot
	File                        worktree.ChangedFile
	Images                      []remote.Image
	Reset                       *agentusage.ResetAttempt
}

type HostSides struct{ Old, New string }
