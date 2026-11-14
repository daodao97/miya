//go:build !mygo_ui

package integration

import (
	"bytes"
	"image"
	"image/png"
	phoneserver "miya/internal/phone/server"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
	"os"
	"path/filepath"
	"testing"
)

func testPNG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSavePhoneImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	data := testPNG(t)
	paths, err := phoneserver.SaveImages([]remote.Image{{Name: "a.png", Data: data}, {Name: "noext", Data: data}})
	if err != nil || len(paths) != 2 {
		t.Fatalf("paths = %v, err = %v", paths, err)
	}
	for _, p := range paths {
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, data) || filepath.Ext(p) != ".png" {
			t.Errorf("%s: saved wrongly (%v)", p, err)
		}
	}
	if _, err := phoneserver.SaveImages([]remote.Image{{Name: "x.txt", Data: []byte("hello")}}); err == nil {
		t.Error("text should be refused")
	}
	if _, err := phoneserver.SaveImages(make([]remote.Image, phonetransfer.MaxImages+1)); err == nil {
		t.Error("too many images should be refused")
	}
}
