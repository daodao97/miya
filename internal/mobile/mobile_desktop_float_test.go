package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop/control"
	phoneproxy "miya/internal/phone/proxy"
	"miya/internal/uiruntime"
)

func TestDesktopFloatTouchDoesNotScrollUnderneath(t *testing.T) {
	m := NewMobileApp()
	m.ApplyPalette(false)
	p := &MobileDesktopPage{Phase: "live", Floating: true}
	m.DesktopPage = p
	var scroll ui.ScrollState
	clicks := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().TrackScroll(&scroll).Children(func() {
			for i := range 80 {
				if ui.Button(c, fmt.Sprintf("Message %d", i)).Height(60).Clicked() {
					clicks++
				}
			}
		})
		m.desktopFloatView(c)
	}, 390, 750)
	tt.UsePointer(ui.PointerTouch, 1)
	tt.Frame()
	scroll.Y = 500
	tt.Frame()
	before := scroll.Y
	label := "Remote desktop floating window. Tap to return; drag to move."
	r, ok := tt.Find(label)
	if !ok || scroll.MaxY <= before {
		t.Fatal("missing float or scrollable background", r, scroll)
	}
	closeRect, _ := tt.Find("End remote desktop")
	x, y := r.X+30, r.Y+30
	tt.Press(x, y)
	tt.Frame()
	// Move the float completely away from the original contact point,
	// then reverse direction while the same contact still owns it.
	for _, d := range []float32{30, 100, 220, 280, 200} {
		tt.Advance(16 * time.Millisecond)
		tt.Move(x-60, y-d)
		tt.Frame()
		got, found := tt.Find(label)
		if !found || math.Abs(float64(got.Y-(r.Y-d))) > 1 {
			t.Fatalf("float lost touch capture at %v: initial=%v got=%v", d, r, got)
		}
		closeNow, _ := tt.Find("End remote desktop")
		if math.Abs(float64((closeNow.X-got.X)-(closeRect.X-r.X))) > 1 ||
			math.Abs(float64((closeNow.Y-got.Y)-(closeRect.Y-r.Y))) > 1 {
			t.Fatal("close button did not move with the float", got, closeNow)
		}
		if scroll.Y != before || clicks != 0 {
			t.Fatalf("float drag reached messages: scroll %v -> %v, clicks=%v", before, scroll.Y, clicks)
		}
	}
	tt.Release(x-60, y-200)
	tt.Frame()
	tt.Advance(time.Second)
	if scroll.Y != before || clicks != 0 || !p.Floating || len(p.Queue) != 0 {
		t.Fatal("release leaked a scroll, click or remote input", scroll.Y, clicks, p.Floating, p.Queue)
	}
	// The list outside the float must still accept normal touch scrolling.
	tt.Press(5, 200)
	tt.Frame()
	tt.Move(5, 100)
	tt.Frame()
	tt.Release(5, 100)
	tt.Frame()
	if scroll.Y <= before {
		t.Fatal("float blocked scrolling outside its bounds", before, scroll.Y, scroll.MaxY, r)
	}
	cancelBefore := scroll.Y
	tt.Frame()
	r, _ = tt.Find(label)
	tt.Press(r.X+30, r.Y+30)
	tt.Move(r.X+60, r.Y-100)
	tt.Frame()
	tt.CancelPress()
	tt.Advance(time.Second)
	if scroll.Y != cancelBefore || clicks != 0 || !p.Floating || len(p.Queue) != 0 {
		t.Fatal("canceled drag leaked input or restored the page")
	}
}

func TestBackgroundPausesFloatingControlWhileKeepingBrowser(t *testing.T) {
	m := NewMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &MobileDesktopPage{Phase: "live", Active: true, Floating: true, Ctx: ctx, Cancel: cancel,
		Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }, "test")}
	m.DesktopPage = p
	browserClosed := false
	m.FilesCancel = func() { browserClosed = true }
	m.enterBackground(true)
	if p.Phase != "paused" || !p.suspended || p.Active || ctx.Err() == nil || browserClosed || m.FilesCancel == nil {
		t.Fatal("background must pause control without dropping the Android browser proxy")
	}
}

func TestFilePreviewCleanupPreservesFloatingApproval(t *testing.T) {
	m := NewMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.DesktopPage = &MobileDesktopPage{Phase: "live", Floating: true, Ctx: ctx, Cancel: cancel,
		Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }, "test")}
	m.DesktopApproval = MobileDesktopApproval{ID: json.RawMessage(`"approval"`), Version: "v1"}
	posts := make(chan func(), 1)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	defer func() { uiruntime.RunOnMain = old }()
	// Invalid URLs make the browser return before invoking a native host.
	proxy := new(phoneproxy.PhoneFileProxy)
	if err := proxy.Start("/project", func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxy.URL = ":invalid"
	m.openFileBrowser(proxy, "")
	select {
	case f := <-posts:
		f()
	case <-time.After(time.Second):
		t.Fatal("browser cleanup did not run")
	}
	if m.FilesCancel != nil {
		t.Fatal("browser resources remained open")
	}
	m.CloseResolvedDesktopControl(nil)
	if m.DesktopPage != nil || ctx.Err() == nil {
		t.Fatal("file preview cleanup lost the approval that owns floating control")
	}
}

func TestDesktopRestoreKeepsOneFramePollingChain(t *testing.T) {
	m := NewMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	posts := make(chan func(), 8)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	defer func() { cancel(); uiruntime.RunOnMain = old }()
	requests := make(chan struct{}, 8)
	p := &MobileDesktopPage{Phase: "live", Active: true, Floating: true, Ctx: ctx, Cancel: cancel,
		Client: control.NewClient(func(context.Context) (net.Conn, error) {
			requests <- struct{}{}
			return nil, errors.New("offline")
		}, "test")}
	m.DesktopPage = p
	m.ReceiveDesktopFrame(p, nil, nil, false, nil)
	m.DesktopRestore(p)
	select {
	case <-requests:
		t.Fatal("restore started another request before the pending frame tick")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case f := <-posts:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("existing frame poll did not continue after restore")
	}
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("pending frame tick did not request a frame")
	}
	// Drain the response before restoring the main-thread dispatcher.
	select {
	case f := <-posts:
		f()
	case <-time.After(time.Second):
		t.Fatal("frame response was not delivered")
	}
}
