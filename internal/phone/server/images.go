package server

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".heic": true, ".heif": true}

// SaveImages keeps a phone's photos as files the agents can read, as
// attachments chosen on the desktop are. HEIC becomes JPEG, which every
// agent reads.
func SaveImages(images []remote.Image) (paths []string, err error) {
	if len(images) == 0 {
		return nil, nil
	}
	if err := phonetransfer.CheckImages(len(images), 0, 0); err != nil {
		return nil, err
	}
	dir := filepath.Join(os.TempDir(), "miya-phone-uploads")
	if base := Dir(); base != "" {
		dir = filepath.Join(base, "uploads")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.New("Unable to save image")
	}
	phonetransfer.PruneOldUploads(dir)
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
		if err := phonetransfer.CheckImages(len(images), len(img.Data), total); err != nil {
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
