package integration

import (
	msgfmt "miya/internal/chat/presentation"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestAttachmentDisplay(t *testing.T) {
	dir := t.TempDir()
	png, doc := filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt")
	os.WriteFile(png, []byte("x"), 0o600)
	os.WriteFile(doc, []byte("x"), 0o600)
	text, images := msgfmt.AttachmentDisplay("hi", []string{png, doc})
	if text != "hi\n📎 notes.txt" || len(images) != 1 || !strings.HasSuffix(images[0], "shot.png") {
		t.Fatalf("%q %q", text, images)
	}
}
