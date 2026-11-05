package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"testing"

	"miya/internal/desktop"
)

func TestDesktopWindowIdentitySelectionAndNoDisplayFallback(t *testing.T) {
	windows := []desktop.Window{{ID: 11, PID: 42, App: "Example", Title: "密码授权"}, {ID: 12, PID: 42, App: "Example", Title: "其他窗口"}}
	d, old, calls := mockDesktop(t)
	d.Revoke(old)
	d.Windows = func() ([]desktop.Window, error) { return windows, nil }
	var captured, received desktop.Window
	d.Capture = func() ([]byte, error) { t.Fatal("window session captured full desktop"); return nil, nil }
	d.Input = func(desktop.Input) error { t.Fatal("window session sent display input"); return nil }
	d.CaptureWindow = func(w desktop.Window) ([]byte, error) { captured = w; return []byte("window pixels"), nil }
	d.InputWindow = func(w desktop.Window, in desktop.Input) error { received = w; calls.Add(1); return nil }
	token, err := d.OpenWindow(context.Background(), &windows[0])
	if err != nil {
		t.Fatal(err)
	}
	defer d.Revoke(token)
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 || captured.ID != 11 {
		t.Fatal(w.Code, captured)
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"text","text":"synthetic-password"}`); w.Code != 204 || received.ID != 11 {
		t.Fatal(w.Code, received)
	}
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":12,"pid":999}`); w.Code != 404 {
		t.Fatal("PID substitution accepted", w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":12,"pid":42,"title":"spoof"}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	w := desktopHTTP(d, token, "GET", "/session", "")
	var session struct {
		WindowOnly bool           `json:"windowOnly"`
		Window     desktop.Window `json:"window"`
	}
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || !session.WindowOnly || session.Window.Title != "其他窗口" {
		t.Fatal(w.Body)
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"key","key":"enter"}`); w.Code != 403 {
		t.Fatal("window switch kept input active", w.Code)
	}
	desktopHTTP(d, token, "POST", "/start", `{}`)
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"key","key":"enter"}`); w.Code != 409 {
		t.Fatal("old window frame authorized new input", w.Code)
	}
	d.CaptureWindow = func(desktop.Window) ([]byte, error) { return nil, errors.New("window closed") }
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 503 {
		t.Fatal("closed window capture accepted", w.Code)
	}
	if calls.Load() != 1 {
		t.Fatal("extra input")
	}
	d.CaptureWindow = func(desktop.Window) ([]byte, error) { return nil, desktop.ErrWindowUnavailable }
	desktopHTTP(d, token, "POST", "/select", `{"id":12,"pid":42}`)
	desktopHTTP(d, token, "POST", "/start", `{}`)
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 410 {
		t.Fatal("closed target did not end control", w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"key","key":"enter"}`); w.Code != 410 {
		t.Fatal("closed target still accepts input", w.Code)
	}
}

func TestDesktopWindowPreviewIsReadOnlyAndMatchesTarget(t *testing.T) {
	password := desktop.Window{ID: 21, PID: 80, App: "系统设置", BundleID: "com.apple.systempreferences", Title: "授权密码"}
	other := desktop.Window{ID: 22, PID: 81, App: "Browser", Title: "私人网页"}
	data := windowJPEG(t)
	d := &Server{
		Windows: func() ([]desktop.Window, error) { return []desktop.Window{other, password}, nil },
		CaptureWindow: func(w desktop.Window) ([]byte, error) {
			if w != password {
				t.Fatal("captured wrong target", w)
			}
			return data, nil
		},
		Authorize: func() error { t.Fatal("preview requested OS permissions"); return nil },
		Capture:   func() ([]byte, error) { t.Fatal("preview captured display"); return nil, nil },
	}
	w, preview, err := d.PreviewWindow(WindowHint{App: password.BundleID, Title: "密码"})
	if err != nil || w != password || len(preview) == 0 || d.lease != nil {
		t.Fatal(w, err)
	}
	if _, _, err = d.PreviewWindow(WindowHint{Window: &desktop.Window{ID: password.ID, PID: 999}}); err == nil {
		t.Fatal("reused window number accepted")
	}
	if _, err = ChooseWindow([]desktop.Window{password, password}, WindowHint{App: password.App}); err == nil {
		t.Fatal("ambiguous window auto-selected")
	}
	if _, err = ChooseWindow([]desktop.Window{other}, WindowHint{Window: &password}); err == nil {
		t.Fatal("closed target fell back to another window")
	}
}

func windowJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 320, 180)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
