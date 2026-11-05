//go:build !windows

package main

import (
	"testing"

	"miya/internal/remote"
)

func TestPhoneCompletionReadUpdatesDesktopWithoutPush(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	c := serverController(t.TempDir(), "")
	c.loadDesktopUnread()
	thread := Thread{ID: 7, ServerID: "session", Agent: agentClaude, CompletedAtMs: 200, HistoryLoaded: true}
	c.projectCache["/background"] = []Thread{thread}
	key := desktopUnreadKey(thread)
	c.desktopUnread[key] = 200
	oldHook := currentInvalidateHook()
	var updates int
	setInvalidateHook(func() { updates++ })
	t.Cleanup(func() { setInvalidateHook(oldHook) })
	conn, ex := serveTestPhone(t, c)
	request := remote.Request{Op: "read", Thread: 999, ServerID: thread.ServerID, Agent: agentClaude, Project: "/background"}
	for _, version := range []int64{0, 100, 300, 200} {
		request.ReadCompletedAtMs = version
		conn.requests <- request
		for {
			select {
			case frame := <-conn.frames:
				if frame.Type != "read" {
					continue
				}
			case <-conn.ctx.Done():
				t.Fatal("phone read was not acknowledged")
			}
			break
		}
		ex.Do(func() {
			if got := c.desktopThreadUnread(thread); got != (version != 200) {
				t.Errorf("version %d: desktop unread = %v", version, got)
			}
			wantUpdates := 0
			if version == 200 {
				wantUpdates = 1
			}
			if updates != wantUpdates {
				t.Errorf("version %d: published %d desktop updates, want %d", version, updates, wantUpdates)
			}
		})
	}
	restored := &chatController{}
	restored.loadDesktopUnread()
	if restored.desktopThreadUnread(thread) {
		t.Fatal("the phone's read did not persist across desktop restarts")
	}
}

func TestPhoneCompletionReadUsesStableIdentity(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.loadDesktopUnread()
	thread := Thread{ID: 1, ServerID: "new-session", CompletedAtMs: 200}
	c.setThreads([]Thread{thread})
	c.desktopUnread[desktopUnreadKey(thread)] = 200
	for _, r := range []remote.Request{
		{Thread: 1, ServerID: "old-session", ReadCompletedAtMs: 200},
		{Thread: 1, ServerID: thread.ServerID, Agent: agentClaude, ReadCompletedAtMs: 200},
		{Thread: 1, ServerID: thread.ServerID, Project: "/wrong", ReadCompletedAtMs: 200},
	} {
		c.markPhoneCompletionRead(r)
		if !c.desktopThreadUnread(thread) {
			t.Fatal("an acknowledgement for another conversation cleared unread")
		}
	}
}
