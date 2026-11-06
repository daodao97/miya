package main

import (
	bytes "bytes"
	base64 "encoding/base64"
	appmodel "miya/internal/appmodel"
	url "net/url"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
	utf8 "unicode/utf8"
)

func TestLargeImageUsesShortFileURL(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	data := bytes.Repeat([]byte{1, 2, 3}, 400000)
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	got := appmodel.ImageSource(src)
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
	if appmodel.ImageSource(src) != got {
		t.Fatal("image cache not reused")
	}
	path := filepath.Join(t.TempDir(), "image with spaces.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := appmodel.LocalImageDataURI(path); !strings.HasPrefix(got, "file://") || strings.Contains(got, " ") {
		t.Fatal(got)
	}
}

func TestNativeTextLimitPreservesUTF8(t *testing.T) {
	text := strings.Repeat("你好", 200000)
	got := appmodel.NativeText(text)
	if len(got) >= 1048576 || !utf8.ValidString(got) || !strings.Contains(got, "Content truncated for display") {
		t.Fatal("invalid bridge text")
	}
	if appmodel.NativeText("short") != "short" {
		t.Fatal("short text changed")
	}
}
