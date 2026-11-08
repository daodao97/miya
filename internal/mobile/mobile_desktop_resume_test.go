package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/uiruntime"
)

type resumeTransport func(*http.Request) (*http.Response, error)

func (f resumeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDesktopBackgroundReconnectRestoresExactWindow(t *testing.T) {
	oldClose := SendDesktopClose
	SendDesktopClose = func(*remote.Conn, string) {}
	t.Cleanup(func() { SendDesktopClose = oldClose })
	m := NewMobileApp()
	m.Link = "miya://paired-mac"
	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewRGBA(image.Rect(0, 0, 120, 80)), nil); err != nil {
		t.Fatal(err)
	}
	window := desktop.Window{ID: 42, PID: 7, App: "Fixture", Title: "Selected window"}
	var inputs atomic.Int32
	d := &control.Server{Native: control.Native{Authorize: func() error { return nil }, Windows: func() ([]desktop.Window, error) { return []desktop.Window{window}, nil }, CaptureWindow: func(w desktop.Window) ([]byte, error) {
		if w.ID != 42 || w.PID != 7 {
			t.Error("wrong window captured")
		}
		return img.Bytes(), nil
	}, InputWindow: func(desktop.Window, desktop.Input) error { inputs.Add(1); return nil }}}
	client := func(_ *remote.Conn, token string) *control.Client {
		c := control.NewClient(func(context.Context) (net.Conn, error) {
			t.Error("unexpected network dial")
			return nil, context.Canceled
		}, token)
		c.SetTransport(resumeTransport(func(r *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			d.ServeHTTP(w, r)
			return w.Result(), nil
		}))
		return c
	}
	m.desktopClient = client
	oldCtx, oldCancel := context.WithCancel(context.Background())
	old := &MobileDesktopPage{Ctx: oldCtx, Cancel: oldCancel, Client: client(nil, "old"), Phase: "live", Active: true, WindowOnly: true, Window: window, Floating: true, Zoom: 2, Trackpad: false,
		owner: remote.Request{Op: "desktop-open", ServerID: "original-conversation", Agent: "codex", Project: "/original", WindowPick: true}, desktopLink: m.Link,
		Queue: []desktop.Input{{Kind: "text", Text: "never replay"}}, text: "unsent secret", Unlock: DesktopUnlockState{Text: "unsent password"}}
	m.DesktopPage = old
	m.enterBackground(false)
	if m.DesktopPage != old || !old.suspended || old.Phase != "paused" || old.Active || oldCtx.Err() == nil || len(old.Queue) != 0 || old.text != "" || old.Unlock.Text != "" {
		t.Fatal("background did not pause and clear live state")
	}
	posts := make(chan func(), 128)
	previous := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	t.Cleanup(func() { m.CloseDesktopPage(false); uiruntime.RunOnMain = previous })
	requests := make(chan remote.Request, 8)
	m.desktopSend = func(_ *remote.Conn, r remote.Request) error { requests <- r; return nil }
	m.Conn, m.Connected = new(remote.Conn), true
	// The user can be reading another conversation behind a floating view.
	m.Open = 99
	m.ApplyPhoneState(phoneproto.State{DesktopControl: true})
	select {
	case <-requests:
		t.Fatal("reopened while backgrounded")
	default:
	}
	m.Cancel = func() {}
	m.enterForeground()
	m.resumeDesktopPage()
	var request remote.Request
	select {
	case request = <-requests:
	case <-time.After(time.Minute):
		t.Fatal("resume request missing")
	}
	if request.ServerID != "original-conversation" || request.Project != "/original" || !request.WindowPick {
		t.Fatalf("resume changed owner: %+v", request)
	}
	if !old.resuming || m.DesktopRequest == "" {
		t.Fatal("resume not tracked")
	}
	select {
	case <-requests:
		t.Fatal("duplicate resume request")
	default:
	}
	token, err := d.OpenWindow(context.Background(), &desktop.Window{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Revoke(token) })
	d.AllowPick(token)
	reply, _ := json.Marshal(phoneproto.DesktopReply{RequestID: request.RequestID, Token: token})
	m.receiveDesktopControl(m.Conn, reply, "")
	guard := time.NewTimer(time.Minute)
	defer guard.Stop()
	for m.DesktopPage.Phase != "live" || m.DesktopPage.Image == nil {
		select {
		case f := <-posts:
			f()
		case <-guard.C:
			t.Fatalf("resume stalled: phase=%s detail=%s", m.DesktopPage.Phase, m.DesktopPage.Detail)
		}
	}
	p := m.DesktopPage
	if p == old || p.Conn != m.Conn || !p.Floating || !p.WindowOnly || p.Window.ID != 42 || p.Window.PID != 7 || len(p.Queue) != 0 || inputs.Load() != 0 {
		t.Fatal("resume lost identity or replayed input")
	}
	// Any old response that was already queued must not finish the new page.
	m.DesktopFinish(old, "late old response")
	if m.DesktopPage != p || p.Phase != "live" {
		t.Fatal("old response ended resumed page")
	}
}

func TestDesktopResumeApprovalAndExplicitClose(t *testing.T) {
	m := NewMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	p := &MobileDesktopPage{Ctx: ctx, Cancel: cancel, Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, context.Canceled }, strings.Repeat("a", 64)), Phase: "live", Active: true}
	m.DesktopPage = p
	m.DesktopApproval = MobileDesktopApproval{ID: json.RawMessage(`"approval"`), Version: "version"}
	m.enterBackground(false)
	if len(m.DesktopApproval.ID) == 0 {
		t.Fatal("background lost pending approval identity")
	}
	m.CloseResolvedDesktopControl(nil)
	if m.DesktopPage != nil {
		t.Fatal("resolved approval retained paused control")
	}
	ctx, cancel = context.WithCancel(context.Background())
	p = &MobileDesktopPage{Ctx: ctx, Cancel: cancel, Client: p.Client, Phase: "live", Active: true}
	m.DesktopPage = p
	m.Disconnect()
	if p.suspended || p.Phase != "ended" {
		t.Fatal("manual disconnect can auto-resume")
	}
}

func TestDesktopResumeRefusesChangedWindowAndDisplay(t *testing.T) {
	for _, kind := range []string{"window", "display"} {
		t.Run(kind, func(t *testing.T) {
			m := NewMobileApp()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &MobileDesktopPage{Ctx: ctx, Cancel: cancel, Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, context.Canceled }, "old"), Phase: "connecting", restoring: true, WindowOnly: kind == "window", Window: desktop.Window{ID: 42, PID: 7}, Display: desktop.Display{ID: 2, UUID: "original-display"}}
			m.DesktopPage = p
			m.initializeResumedDesktop(p, control.Session{Pick: true, Displays: true}, []desktop.Display{{ID: 2, UUID: "replacement-display"}}, []desktop.Window{{ID: 42, PID: 8}}, nil)
			if kind == "window" && p.Phase != "ended" {
				t.Fatal("changed window owner was resumed")
			}
			if kind == "display" && (p.Phase != "picker" || p.Active) {
				t.Fatal("changed display selected a fallback")
			}
		})
	}
}

func TestDesktopCanceledWorkCannotApplyAfterBackground(t *testing.T) {
	m := NewMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &MobileDesktopPage{Ctx: ctx, Cancel: cancel, Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, context.Canceled }, "old"), Phase: "live", Active: true}
	m.DesktopPage = p
	posts := make(chan func(), 1)
	previous := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	defer func() { uiruntime.RunOnMain = previous }()
	applied := false
	m.desktopDo(p, func(context.Context) func() { return func() { applied = true } })
	var queued func()
	select {
	case queued = <-posts:
	case <-time.After(time.Minute):
		t.Fatal("work was not queued")
	}
	m.enterBackground(false)
	queued()
	if applied || p.Phase != "paused" {
		t.Fatal("old queued result applied while paused")
	}
}
