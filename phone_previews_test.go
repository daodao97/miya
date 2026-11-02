package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

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
	preview, err := phonePreview(localImageDataURI(path))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil || config.Width != phonePreviewEdge || config.Height != phonePreviewEdge/2 || len(preview) > maxPhonePreviewBytes {
		t.Fatalf("invalid thumbnail: %+v, bytes=%d, err=%v", config, len(preview), err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(original, after) {
		t.Fatal("thumbnail generation modified the original")
	}
}

func TestPhonePreviewSurvivesSendAndRelaunch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	local := filepath.Join(t.TempDir(), "selected.png")
	if err := os.WriteFile(local, testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	upload, err := readPhoneImages([]string{local})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := savePhoneImages(upload)
	if err != nil {
		t.Fatal(err)
	}
	source := localImageDataURI(paths[0])
	preview, err := phonePreview(source)
	if err != nil {
		t.Fatal(err)
	}
	// Dispatch the asynchronous file work deterministically on this thread.
	calls := make(chan func(), 8)
	oldDispatch := runOnMain
	runOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { runOnMain = oldDispatch })
	next := func() {
		t.Helper()
		select {
		case f := <-calls:
			f()
		case <-time.After(time.Minute):
			t.Fatal("preview file work did not complete")
		}
	}
	m := testMobileApp()
	m.dataDir = t.TempDir()
	m.openDiskCache(m.link)
	t.Cleanup(func() { m.flushCache(true) })
	th := m.state.Projects[0].Threads[0]
	th.HistoryLoaded = true
	th.Messages = []Message{{ID: 10, Role: "user", Images: []string{source}, Content: "look"}}
	m.openThread(th)
	sync := newPhoneThreadSync(nil)
	delta, _ := sync.update(th)
	m.receiveThread(th.ID, delta)
	m.images, m.sending = []string{local}, true
	m.sent(th.ID, "")
	m.receiveImage(phoneImagePreview{Source: source, Data: preview})
	next()
	if m.messageImage(source) == nil || len(m.images) != 0 {
		t.Fatal("sent photo has no preview after composer attachments were cleared")
	}
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if _, ok := tt.Find("图片预览"); !ok {
		t.Fatal("sent image is missing from the conversation")
	}
	m.flushCache(true)
	// An offline relaunch uses its own bytes, even without the desktop file.
	os.Remove(paths[0])
	n := testMobileApp()
	n.connected = false
	n.dataDir = m.dataDir
	n.openDiskCache(n.link)
	imageMu.Lock()
	delete(imageCache, n.previewPath(source))
	imageMu.Unlock()
	if n.messageImage(source) != nil {
		t.Fatal("disk lookup should run off the UI thread")
	}
	next()
	if _, err := loadImage(n.previewPaths[source]); err != nil || n.messageImage(source) == nil {
		t.Fatalf("offline relaunch lost its preview: %v", err)
	}
	if got := n.cachedMessages(th.ServerID); len(got) != 1 || got[0].Images[0] != source {
		t.Fatal("preview caching changed message identities")
	}
	if err := storePhonePreview(filepath.Join(t.TempDir(), "invalid.jpg"), []byte("not an image")); err == nil {
		t.Fatal("invalid image bytes were accepted")
	}
}

func TestPhoneDeltaDetectsChangedImageSource(t *testing.T) {
	th := longThread(1)
	th.Messages[0].Images = []string{"file:///desktop/first.png"}
	sync := newPhoneThreadSync(nil)
	sync.update(th)
	th.Messages[0].Images = []string{"file:///desktop/other.png"}
	delta, changed := sync.update(th)
	if !changed || len(delta.Changed) != 1 || !sync.images[th.Messages[0].Images[0]] || sync.images["file:///desktop/first.png"] {
		t.Fatal("image replacement did not update the phone")
	}
}

func TestPhonePreviewRetryReleasesPendingButKeepsCompletedCache(t *testing.T) {
	calls := make(chan func(), 4)
	old := runOnMain
	runOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { runOnMain = old })
	m := testMobileApp()
	conn := new(remote.Conn)
	m.conn, m.connected = conn, true
	m.previewPaths = map[string]string{"pending": "", "completed": ""}
	m.retryImage("pending", conn)
	m.retryImage("completed", conn)
	m.previewPaths["completed"] = "cached.jpg"
	for range 2 {
		select {
		case f := <-calls:
			f()
		case <-time.After(3 * time.Second):
			t.Fatal("retry never ran")
		}
	}
	if _, ok := m.previewPaths["pending"]; ok {
		t.Fatal("failed preview still suppresses a fresh request")
	}
	if m.previewPaths["completed"] != "cached.jpg" {
		t.Fatal("retry discarded a completed preview")
	}
}

func TestPhonePreviewRetryDoesNotTouchReconnectedSession(t *testing.T) {
	calls := make(chan func(), 2)
	old := runOnMain
	runOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { runOnMain = old })
	m := testMobileApp()
	conn := new(remote.Conn)
	m.conn, m.connected = conn, true
	m.previewPaths = map[string]string{"photo": ""}
	m.retryImage("photo", conn)
	m.conn = new(remote.Conn)
	select {
	case f := <-calls:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("retry never ran")
	}
	if _, ok := m.previewPaths["photo"]; !ok {
		t.Fatal("old transfer invalidated the new session")
	}
	m.previewRetries = map[string]int{"photo": 3}
	m.retryImage("photo", m.conn)
	if m.previewRetries["photo"] != 3 {
		t.Fatal("retry budget was not enforced")
	}
}
