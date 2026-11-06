package server

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/appdir"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
)

func CheckDesktopUploads(count, size, total int) error {
	if count > phonetransfer.MaxImages || size > phonetransfer.MaxImageBytes || total > phonetransfer.MaxImageTotal {
		return fmt.Errorf("Send up to %d attachments at a time, %d MB each, %d MB total", phonetransfer.MaxImages, phonetransfer.MaxImageBytes>>20, phonetransfer.MaxImageTotal>>20)
	}
	return nil
}

func ReadDesktopUploads(paths []string) ([]remote.Image, error) {
	if err := CheckDesktopUploads(len(paths), 0, 0); err != nil {
		return nil, err
	}
	var out []remote.Image
	total := 0
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, phonetransfer.MaxImageBytes+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		total += len(data)
		if err := CheckDesktopUploads(len(paths), len(data), total); err != nil {
			return nil, err
		}
		out = append(out, remote.Image{Name: filepath.Base(path), Data: data})
	}
	return out, nil
}

func SaveDesktopUploads(files []remote.Image) (paths []string, err error) {
	if err := CheckDesktopUploads(len(files), 0, 0); err != nil {
		return nil, err
	}
	dir := filepath.Join(appdir.Dir(), "uploads")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	phonetransfer.PruneOldUploads(dir)
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
		if err := CheckDesktopUploads(len(files), len(file.Data), total); err != nil {
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
