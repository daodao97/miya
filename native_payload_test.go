package main

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLargeImageUsesShortFileURL(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	data := bytes.Repeat([]byte{1, 2, 3}, 400000)
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	got := imageSource(src)
	if !strings.HasPrefix(got, "file://") || len(got) > 4096 {
		t.Fatalf("oversized image bridge value: %d", len(got))
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := os.ReadFile(u.Path)
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatal("image data was lost", err)
	}
	if imageSource(src) != got {
		t.Fatal("image cache not reused")
	}
	path := filepath.Join(t.TempDir(), "image with spaces.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := localImageDataURI(path); !strings.HasPrefix(got, "file://") || strings.Contains(got, " ") {
		t.Fatal(got)
	}
}

func TestNativeTextLimitPreservesUTF8(t *testing.T) {
	text := strings.Repeat("你好", 200000)
	got := nativeText(text)
	if len(got) >= 1048576 || !utf8.ValidString(got) || !strings.Contains(got, "显示已截断") {
		t.Fatal("invalid bridge text")
	}
	if nativeText("short") != "short" {
		t.Fatal("short text changed")
	}
}
