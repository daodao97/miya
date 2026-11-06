package transcript

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mime"
	"os"
	"path/filepath"

	chattext "miya/internal/chat/text"
	"miya/internal/fsutil"
)

// claudeImageSource is an image block's source in Claude Code's sessions.
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func ImageDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Miya", "claude-images")
}

func HasImage(blocks []Block) bool {
	for _, b := range blocks {
		if b.Type == "image" && b.Source != nil && b.Source.Data != "" {
			return true
		}
	}
	return false
}

// claudeImages writes a prompt's images to the cache, once per content, and
// returns them as file URLs: messages carry paths, never megabytes of base64
// across the service's socket and into saved histories.
func Images(blocks []Block) []string {
	var out []string
	for _, b := range blocks {
		if b.Type != "image" || b.Source == nil || b.Source.Type != "base64" || b.Source.Data == "" {
			continue
		}
		sum := sha256.Sum256([]byte(b.Source.Data))
		ext := ".png"
		if exts, _ := mime.ExtensionsByType(b.Source.MediaType); len(exts) > 0 {
			ext = exts[0]
		}
		if b.Source.MediaType == "image/jpeg" {
			ext = ".jpg"
		}
		path := filepath.Join(ImageDir(), hex.EncodeToString(sum[:16])+ext)
		if _, err := os.Stat(path); err != nil {
			data, err := base64.StdEncoding.DecodeString(b.Source.Data)
			if err != nil {
				continue
			}
			if os.MkdirAll(filepath.Dir(path), 0o700) != nil || fsutil.WriteFileAtomic(path, func(f *os.File) error { _, err := f.Write(data); return err }) != nil {
				continue
			}
		}
		if uri := chattext.LocalImageURI(path); uri != "" {
			out = append(out, uri)
		}
	}
	return out
}
