package files

import (
	"errors"
	"os"
	"path/filepath"
)

func ProjectPath(root, rel string) (string, error) {
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
