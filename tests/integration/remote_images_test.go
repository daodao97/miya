package integration

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	phoneserver "miya/internal/phone/server"
	"miya/internal/remote"
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
	if _, err := phoneserver.SaveImages(make([]remote.Image, phoneserver.MaxImages+1)); err == nil {
		t.Error("too many images should be refused")
	}
}

func TestMobileComposerAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	os.WriteFile(path, testPNG(t), 0o600)
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("Add image"); !ok {
		t.Fatal("the composer should offer to add photos")
	}
	m.Images = []string{path}
	tt.Frame()
	if err := tt.Click("Remove image"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(m.Images) != 0 {
		t.Fatalf("removing should drop the photo: %v", m.Images)
	}
}
