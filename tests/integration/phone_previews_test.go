package integration

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

	"miya/internal/agent/claude/native/transcript"
	"miya/internal/mobile"
	"miya/internal/model"
	phoneserver "miya/internal/phone/server"
	"miya/internal/remote"
	"miya/internal/thumbnail"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

func TestPhonePreviewSurvivesSendAndRelaunch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	local := filepath.Join(t.TempDir(), "selected.png")
	if err := os.WriteFile(local, testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	upload, err := phoneserver.ReadImages([]string{local})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := phoneserver.SaveImages(upload)
	if err != nil {
		t.Fatal(err)
	}
	source := transcript.LocalImageURI(paths[0])
	preview, err := thumbnail.File(source)
	if err != nil {
		t.Fatal(err)
	}
	// Dispatch the asynchronous file work deterministically on this thread.
	calls := make(chan func(), 8)
	oldDispatch := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { uiruntime.RunOnMain = oldDispatch })
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
	m.DataDir = t.TempDir()
	m.OpenDiskCache(m.Link)
	t.Cleanup(func() { m.FlushCache(true) })
	th := m.State.Projects[0].Threads[0]
	th.HistoryLoaded = true
	th.Messages = []model.Message{{ID: 10, Role: "user", Images: []string{source}, Content: "look"}}
	m.OpenThread(th)
	sync := phoneserver.NewThreadSync(nil)
	delta, _ := sync.Update(th)
	m.ReceiveThread(th.ID, delta)
	m.Images, m.Sending = []string{local}, true
	m.Sent(th.ID, "")
	m.ReceiveImage(thumbnail.Image{Source: source, Data: preview})
	next()
	if m.MessageImage(source) == nil || len(m.Images) != 0 {
		t.Fatal("sent photo has no preview after composer attachments were cleared")
	}
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if _, ok := tt.Find("Image preview"); !ok {
		t.Fatal("sent image is missing from the conversation")
	}
	m.FlushCache(true)
	// An offline relaunch uses its own bytes, even without the desktop file.
	os.Remove(paths[0])
	n := testMobileApp()
	n.Connected = false
	n.DataDir = m.DataDir
	n.OpenDiskCache(n.Link)
	widgets.ImageMu.Lock()
	delete(widgets.ImageCache, n.PreviewPath(source))
	widgets.ImageMu.Unlock()
	if n.MessageImage(source) != nil {
		t.Fatal("disk lookup should run off the UI thread")
	}
	next()
	if _, err := widgets.LoadImage(n.PreviewPaths[source]); err != nil || n.MessageImage(source) == nil {
		t.Fatalf("offline relaunch lost its preview: %v", err)
	}
	if got := n.CachedMessages(th.ServerID); len(got) != 1 || got[0].Images[0] != source {
		t.Fatal("preview caching changed message identities")
	}
	if err := mobile.StorePhonePreview(filepath.Join(t.TempDir(), "invalid.jpg"), []byte("not an image")); err == nil {
		t.Fatal("invalid image bytes were accepted")
	}
}

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
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { uiruntime.RunOnMain = old })
	m := testMobileApp()
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
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { calls <- f }
	t.Cleanup(func() { uiruntime.RunOnMain = old })
	m := testMobileApp()
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
	thumb, err := thumbnail.File(transcript.LocalImageURI(path))
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
