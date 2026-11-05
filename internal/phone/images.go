package phone

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".heic": true, ".heif": true}

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

// readPhoneImages reads the photos a phone attaches to a message.
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

// savePhoneImages keeps a phone's photos as files the agents can read, as
// attachments chosen on the desktop are. HEIC becomes JPEG, which every
// agent reads.
func SaveImages(images []remote.Image) (paths []string, err error) {
	if len(images) == 0 {
		return nil, nil
	}
	if err := CheckImages(len(images), 0, 0); err != nil {
		return nil, err
	}
	dir := filepath.Join(os.TempDir(), "miya-phone-uploads")
	if base := Dir(); base != "" {
		dir = filepath.Join(base, "uploads")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.New("Unable to save image")
	}
	PruneOldUploads(dir)
	defer func() {
		if err != nil {
			for _, p := range paths {
				os.Remove(p)
			}
			paths = nil
		}
	}()
	total := 0
	for _, img := range images {
		total += len(img.Data)
		if err := CheckImages(len(images), len(img.Data), total); err != nil {
			return paths, err
		}
		ext := strings.ToLower(filepath.Ext(filepath.Base(img.Name)))
		if !imageExts[ext] {
			switch http.DetectContentType(img.Data) {
			case "image/png":
				ext = ".png"
			case "image/jpeg":
				ext = ".jpg"
			case "image/gif":
				ext = ".gif"
			case "image/webp":
				ext = ".webp"
			default:
				return paths, errors.New("Unsupported image format")
			}
		}
		f, ferr := os.CreateTemp(dir, "phone-*"+ext)
		if ferr != nil {
			return paths, errors.New("Unable to save image")
		}
		path := f.Name()
		_, werr := f.Write(img.Data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(path)
			return paths, errors.New("Unable to save image")
		}
		if (ext == ".heic" || ext == ".heif") && runtime.GOOS == "darwin" {
			jpg := strings.TrimSuffix(path, ext) + ".jpg"
			if exec.Command("sips", "-s", "format", "jpeg", path, "--out", jpg).Run() == nil {
				os.Remove(path)
				path = jpg
			}
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// pruneOldUploads removes photos sent long ago; the conversations keep
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
