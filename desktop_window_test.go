package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/remote"
)

func windowJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 320, 180)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDesktopWindowIdentitySelectionAndNoDisplayFallback(t *testing.T) {
	windows := []desktop.Window{{ID: 11, PID: 42, App: "Example", Title: "密码授权"}, {ID: 12, PID: 42, App: "Example", Title: "其他窗口"}}
	d, old, calls := mockDesktop(t)
	d.revoke(old)
	d.windows = func() ([]desktop.Window, error) { return windows, nil }
	var captured, received desktop.Window
	d.capture = func() ([]byte, error) { t.Fatal("window session captured full desktop"); return nil, nil }
	d.input = func(desktop.Input) error { t.Fatal("window session sent display input"); return nil }
	d.captureWindow = func(w desktop.Window) ([]byte, error) { captured = w; return []byte("window pixels"), nil }
	d.inputWindow = func(w desktop.Window, in desktop.Input) error { received = w; calls.Add(1); return nil }
	token, err := d.openWindow(context.Background(), &windows[0])
	if err != nil {
		t.Fatal(err)
	}
	defer d.revoke(token)
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
	d.captureWindow = func(desktop.Window) ([]byte, error) { return nil, errors.New("window closed") }
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 503 {
		t.Fatal("closed window capture accepted", w.Code)
	}
	if calls.Load() != 1 {
		t.Fatal("extra input")
	}
	d.captureWindow = func(desktop.Window) ([]byte, error) { return nil, desktop.ErrWindowUnavailable }
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
	d := &desktopControl{
		windows: func() ([]desktop.Window, error) { return []desktop.Window{other, password}, nil },
		captureWindow: func(w desktop.Window) ([]byte, error) {
			if w != password {
				t.Fatal("captured wrong target", w)
			}
			return data, nil
		},
		authorize: func() error { t.Fatal("preview requested OS permissions"); return nil },
		capture:   func() ([]byte, error) { t.Fatal("preview captured display"); return nil, nil },
	}
	w, preview, err := d.previewWindow(desktopWindowHint{App: password.BundleID, Title: "密码"})
	if err != nil || w != password || len(preview) == 0 || d.lease != nil {
		t.Fatal(w, err)
	}
	if _, _, err = d.previewWindow(desktopWindowHint{Window: &desktop.Window{ID: password.ID, PID: 999}}); err == nil {
		t.Fatal("reused window number accepted")
	}
	if _, err = chooseDesktopWindow([]desktop.Window{password, password}, desktopWindowHint{App: password.App}); err == nil {
		t.Fatal("ambiguous window auto-selected")
	}
	if _, err = chooseDesktopWindow([]desktop.Window{other}, desktopWindowHint{Window: &password}); err == nil {
		t.Fatal("closed target fell back to another window")
	}
}

func TestDesktopWindowPreviewRequiresPendingApprovalInConversation(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.setThreads([]Thread{{ID: 1, ServerID: "one"}, {ID: 2, ServerID: "two"}})
	a := codex.Approval{ID: json.RawMessage(`"password"`), ThreadID: "one", Method: "miya/desktopControl", Reason: "密码填写"}
	c.setApprovals([]codex.Approval{a})
	r := remote.Request{ServerID: "one", Approval: a.ID, ApprovalVersion: approvalFingerprint(a)}
	if !c.phoneDesktopApproval(r) {
		t.Fatal("valid approval rejected")
	}
	r.ServerID = "two"
	if c.phoneDesktopApproval(r) {
		t.Fatal("cross-conversation preview accepted")
	}
	r.ServerID = "one"
	r.ApprovalVersion = "stale"
	if c.phoneDesktopApproval(r) {
		t.Fatal("stale approval accepted")
	}
	r.ApprovalVersion = approvalFingerprint(a)
	c.setApprovals(nil)
	if c.phoneDesktopApproval(r) {
		t.Fatal("completed approval preview accepted")
	}
}

func TestDesktopMobileAuthorizationWindowPreview(t *testing.T) {
	img, err := ui.DecodeImage(windowJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.state.DesktopControl = true
		m.state.DesktopWindows = true
		th := m.state.Projects[0].Threads[0]
		m.openThread(th)
		m.showThread(th)
		a := codex.Approval{ID: json.RawMessage(`"password"`), ThreadID: th.ServerID, Method: "miya/desktopControl", Reason: "填写系统密码"}
		m.state.Approvals = []codex.Approval{a}
		m.desktopPreviews = map[string]*mobileWindowPreview{string(a.ID): {version: approvalFingerprint(a), image: img, window: desktop.Window{ID: 21, PID: 80, App: "系统设置", Title: "系统设置 · 授权密码"}, dialog: desktop.Dialog{Title: "要允许此应用作为管理员访问吗？", Message: "输入你的密码以继续。", Actions: []desktop.Action{{ID: "允许访问", Label: "允许访问", Enabled: false, Default: true}, {ID: "不允许", Label: "不允许", Enabled: true, Cancel: true}}}}}
		tt := ui.NewTester(m.viewRoot, width, 844)
		tt.Frame()
		for _, label := range []string{"操作授权窗口", "填写授权信息", "不允许"} {
			r, ok := tt.Find(label)
			if !ok || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) {
				t.Fatalf("preview action missing at %d: %s %v", width, label, r)
			}
		}
		for _, label := range []string{"选择窗口", "已完成，继续", "刷新预览"} {
			if _, ok := tt.Find(label); ok {
				t.Fatalf("extra control rendered: %s", label)
			}
		}
		saveMobileNativeSnapshot(t, tt, "authorization-window-preview")
		m.approvalAnswered(a.ID, "")
		if len(m.desktopPreviews) != 0 {
			t.Fatal("completed password window preview retained")
		}
	}
}
