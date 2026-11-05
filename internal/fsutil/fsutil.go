// Package fsutil holds file writing helpers shared across Miya.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes path through a temporary file in the same
// directory and renames it into place, keeping an existing file's mode.
func WriteFileAtomic(path string, write func(f *os.File) error) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// WritePrivateFile writes data to path through a temporary file created
// with owner-only permissions, then renames it into place.
func WritePrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".miya-private-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
