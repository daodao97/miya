package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestClaudeSessionImagesShowInUserMessages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := "/work/demo"
	img := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + onePixelPNG + `"}}`
	path := writeClaudeSession(t, cwd, "s1",
		`{"type":"user","uuid":"u1","cwd":"/work/demo","message":{"content":[{"type":"text","text":"look at these"},`+img+`,`+img+`]}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"text","text":"seen"}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","message":{"content":[`+img+`]}}`,
	)
	messages, err := parseClaudeSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || len(messages[0].Images) != 2 || messages[0].Content != "look at these" || messages[2].Role != "user" || len(messages[2].Images) != 1 {
		t.Fatalf("messages %+v", messages)
	}
	for _, uri := range messages[0].Images {
		if !strings.HasPrefix(uri, "file://") || strings.Contains(uri, onePixelPNG) {
			t.Fatalf("image not cached as a file: %q", uri)
		}
		if _, err := loadImage(uri); err != nil {
			t.Fatalf("cached image unreadable: %v", err)
		}
	}
	// The image-only prompt counts as a prompt for rewinds too.
	if _, err := forkClaudeSessionBefore(path, 1); err != nil {
		t.Fatalf("fork before the image-only prompt: %v", err)
	}
}

func TestAttachmentDisplay(t *testing.T) {
	dir := t.TempDir()
	png, doc := filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt")
	os.WriteFile(png, []byte("x"), 0o600)
	os.WriteFile(doc, []byte("x"), 0o600)
	text, images := attachmentDisplay("hi", []string{png, doc})
	if text != "hi\n📎 notes.txt" || len(images) != 1 || !strings.HasSuffix(images[0], "shot.png") {
		t.Fatalf("%q %q", text, images)
	}
}
