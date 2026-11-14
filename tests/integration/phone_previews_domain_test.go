package integration

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	chattext "miya/internal/chat/text"
	phoneserver "miya/internal/phone/server"
	"miya/internal/remote"
	"miya/internal/thumbnail"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhoneDeltaDetectsChangedImageSource(t *testing.T) {
	th := longThread(1)
	th.Messages[0].Images = []string{"file:///desktop/first.png"}
	sync := phoneserver.NewThreadSync(nil)
	sync.Update(th)
	th.Messages[0].Images = []string{"file:///desktop/other.png"}
	delta, changed := sync.Update(th)
	if !changed || len(delta.Changed) != 1 || !sync.HasImage(th.Messages[0].Images[0]) || sync.HasImage("file:///desktop/first.png") {
		t.Fatal("image replacement did not update the phone")
	}
}

func TestPhonePreviewRetryReleasesPendingButKeepsCompletedCache(t *testing.T) {
	calls := make(chan func(), 4)
	m := testPhoneClient()
	m.Dispatch = func(f func()) { calls <- f }
	conn := new(remote.Conn)
	m.Conn, m.Connected = conn, true
	m.PreviewPaths = map[string]string{"pending": "", "completed": ""}
	m.RetryImage("pending", conn)
	m.RetryImage("completed", conn)
	m.PreviewPaths["completed"] = "cached.jpg"
	for range 2 {
		select {
		case f := <-calls:
			f()
		case <-time.After(3 * time.Second):
			t.Fatal("retry never ran")
		}
	}
	if _, ok := m.PreviewPaths["pending"]; ok {
		t.Fatal("failed preview still suppresses a fresh request")
	}
	if m.PreviewPaths["completed"] != "cached.jpg" {
		t.Fatal("retry discarded a completed preview")
	}
}

func TestPhonePreviewRetryDoesNotTouchReconnectedSession(t *testing.T) {
	calls := make(chan func(), 2)
	m := testPhoneClient()
	m.Dispatch = func(f func()) { calls <- f }
	conn := new(remote.Conn)
	m.Conn, m.Connected = conn, true
	m.PreviewPaths = map[string]string{"photo": ""}
	m.RetryImage("photo", conn)
	m.Conn = new(remote.Conn)
	select {
	case f := <-calls:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("retry never ran")
	}
	if _, ok := m.PreviewPaths["photo"]; !ok {
		t.Fatal("old transfer invalidated the new session")
	}
	m.PreviewRetries = map[string]int{"photo": 3}
	m.RetryImage("photo", m.Conn)
	if m.PreviewRetries["photo"] != 3 {
		t.Fatal("retry budget was not enforced")
	}
}

func TestPhonePreviewIsBoundedAndKeepsOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large image.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	img.Set(10, 10, color.RGBA{R: 255, A: 255})
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	original, _ := os.ReadFile(path)
	thumb, err := thumbnail.File(chattext.LocalImageURI(path))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || config.Width != thumbnail.Edge || config.Height != thumbnail.Edge/2 || len(thumb) > thumbnail.MaxBytes {
		t.Fatalf("invalid thumbnail: %+v, bytes=%d, err=%v", config, len(thumb), err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(original, after) {
		t.Fatal("thumbnail generation modified the original")
	}
}
