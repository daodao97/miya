package client

import (
	"context"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"net"
	"testing"
	"time"
)

func TestNSGoDesktopResumeRefusesChangedWindowAndDisplay(t *testing.T) {
	for _, kind := range []string{"window", "display"} {
		t.Run(kind, func(t *testing.T) {
			m := New()
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

func TestNSGoDesktopCanceledWorkCannotApplyAfterBackground(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &MobileDesktopPage{Ctx: ctx, Cancel: cancel, Client: control.NewClient(func(context.Context) (net.Conn, error) { return nil, context.Canceled }, "old"), Phase: "live", Active: true}
	m.DesktopPage = p
	posts := make(chan func(), 1)
	m.Dispatch = func(f func()) { posts <- f }

	applied := false
	m.desktopDo(p, func(context.Context) func() { return func() { applied = true } })
	var queued func()
	select {
	case queued = <-posts:
	case <-time.After(time.Minute):
		t.Fatal("work was not queued")
	}
	m.EnterBackground()
	queued()
	if applied || p.Phase != "paused" {
		t.Fatal("old queued result applied while paused")
	}
}
