package control

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"testing"

	"miya/internal/desktop"
)

func pickTestDesktop(t *testing.T, pick bool) (*Server, string) {
	t.Helper()
	d, token, _ := mockDesktop(t)
	d.Windows = func() ([]desktop.Window, error) {
		return []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}}, nil
	}
	d.CaptureWindow = func(desktop.Window) ([]byte, error) { return []byte("window"), nil }
	d.mu.Lock()
	d.lease.window = &desktop.Window{}
	d.mu.Unlock()
	if pick {
		d.AllowPick(token)
	}
	return d, token
}

func TestDesktopMenuLeaseChoosesWindowOrFullDesktop(t *testing.T) {
	d, token := pickTestDesktop(t, true)
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 409 {
		t.Fatal("started before a window was chosen", w.Code)
	}
	if w := desktopHTTP(d, token, "GET", "/windows", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":5,"pid":9}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 || w.Body.String() != "window" {
		t.Fatal("window capture", w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/actions", ""); w.Code != 403 {
		t.Fatal("menu lease exposed authorization actions", w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":0,"pid":0}`); w.Code != 204 {
		t.Fatal("full desktop not selectable", w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 403 {
		t.Fatal("switching kept the previous consent", w.Code)
	}
	var session Session
	w := desktopHTTP(d, token, "GET", "/session", "")
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || session.WindowOnly || !session.Pick {
		t.Fatal("session after full desktop", w.Body.String())
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 || w.Body.String() != "jpeg" {
		t.Fatal("full desktop capture", w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/windows", ""); w.Code != 200 {
		t.Fatal("menu lease cannot return to window choice", w.Code)
	}
}

func TestDesktopAuthorizationLeaseCannotWidenToFullDesktop(t *testing.T) {
	d, token := pickTestDesktop(t, false)
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":0,"pid":0}`); w.Code != 404 {
		t.Fatal("authorization lease widened to the full desktop", w.Code)
	}
	var session Session
	w := desktopHTTP(d, token, "GET", "/session", "")
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || !session.WindowOnly || session.Pick {
		t.Fatal("authorization session", w.Body.String())
	}
}

func TestDesktopMenuLeasePreviewsListedWindowsOnly(t *testing.T) {
	d, token := pickTestDesktop(t, true)
	large := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, large, nil); err != nil {
		t.Fatal(err)
	}
	d.CaptureWindow = func(desktop.Window) ([]byte, error) { return buf.Bytes(), nil }
	w := desktopHTTP(d, token, "GET", "/thumbnail?id=5&pid=9", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal(w.Code, w.Body.String())
	}
	thumb, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil || thumb.Bounds().Dx() != desktopThumbnailSize || thumb.Bounds().Dy() != 270 {
		t.Fatal("thumbnail was not reduced", err)
	}
	if w := desktopHTTP(d, token, "GET", "/thumbnail?id=6&pid=9", ""); w.Code != 404 {
		t.Fatal("unlisted window previewed", w.Code)
	}
	auth, authToken := pickTestDesktop(t, false)
	if w := desktopHTTP(auth, authToken, "GET", "/thumbnail?id=5&pid=9", ""); w.Code != 403 {
		t.Fatal("authorization lease previewed another window", w.Code)
	}
}
