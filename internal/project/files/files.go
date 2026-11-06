package files

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"miya/locales"
)

type FileEntry struct {
	Name, Rel string
	Dir       bool
}

// ReadDirEntries lists a directory of the workspace, folders first.
func ReadDirEntries(root, rel string) ([]FileEntry, error) {
	entries, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		dir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(root, rel, name)); err == nil {
				dir = info.IsDir()
			}
		}
		out = append(out, FileEntry{Name: name, Rel: filepath.ToSlash(filepath.Join(rel, name)), Dir: dir})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

const MaxEditableFile = 4 << 20

func IsBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

// ReadTextFile reads a file the editor can show.
func ReadTextFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > MaxEditableFile {
		return "", fmt.Errorf(tr("File too large (%.1f MB) to open here"), float64(info.Size())/(1<<20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if IsBinary(data) {
		return "", errors.New(tr("Cannot display binary files"))
	}
	return string(data), nil
}

const maxPreviewImage = 16 << 20

// ImageFileKind says how the panel shows a picture: "svg" for an SVG,
// which is text too and can be edited, "bitmap" for the formats
// ui.DecodeBitmap reads, and "" for any other file.
func ImageFileKind(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".svg":
		return "svg"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return "bitmap"
	}
	return ""
}

// ReadImageFile reads a bitmap for the panel to preview.
func ReadImageFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxPreviewImage {
		return nil, fmt.Errorf(tr("Image too large (%.1f MB) to preview here"), float64(info.Size())/(1<<20))
	}
	return os.ReadFile(path)
}

func tr(s string) string { return locales.T(s) }
