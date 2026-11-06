// Package transfer shares attachment limits, image loading and cleanup
// between the phone client and the desktop receiver.
package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"miya/internal/remote"
)

// The same limits apply to binary uploads and legacy inline photos.
const (
	MaxImages     = remote.MaxImages
	MaxImageBytes = remote.MaxImageBytes
	MaxImageTotal = remote.MaxImageTotal
	uploadsKept   = 7 * 24 * time.Hour
)

func CheckImages(n, size, total int) error {
	switch {
	case n > MaxImages:
		return fmt.Errorf("Send up to %d images at a time", MaxImages)
	case size > MaxImageBytes:
		return fmt.Errorf("Each image must be at most %d MB", MaxImageBytes>>20)
	case total > MaxImageTotal:
		return fmt.Errorf("Total image size must be at most %d MB", MaxImageTotal>>20)
	}
	return nil
}

// ReadImages reads the photos a phone attaches to a message.
func ReadImages(paths []string) ([]remote.Image, error) {
	if err := CheckImages(len(paths), 0, 0); err != nil {
		return nil, err
	}
	var out []remote.Image
	total := 0
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, errors.New("Unable to read image. Select it again.")
		}
		total += len(data)
		if err := CheckImages(len(paths), len(data), total); err != nil {
			return nil, err
		}
		out = append(out, remote.Image{Name: filepath.Base(p), Data: data})
	}
	return out, nil
}

// PruneOldUploads removes photos sent long ago; the conversations keep
// their own copies.
func PruneOldUploads(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > uploadsKept {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
