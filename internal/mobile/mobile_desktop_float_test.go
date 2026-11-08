package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"miya/internal/desktop/control"
	phoneproxy "miya/internal/phone/proxy"
	"miya/internal/uiruntime"
)

func TestBackgroundEndsFloatingControlWhileKeepingBrowser(t *testing.T) {
	m := NewMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &MobileDesktopPage{Phase: "live", Active: true, Floating: true, Ctx: ctx, Cancel: cancel,
		Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, errors.New("offline") }, "test")}
	m.DesktopPage = p
	browserClosed := false
	m.FilesCancel = func() { browserClosed = true }
	m.enterBackground(true)
	if p.Phase != "ended" || p.Active || ctx.Err() == nil || browserClosed || m.FilesCancel == nil {
		t.Fatal("background must end control without dropping the Android browser proxy")
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
