//go:build !windows

package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/remote"
	"miya/internal/uiruntime"
)

func TestPhoneCompletionReadUpdatesDesktopWithoutPush(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	c := serverController(t.TempDir(), "")
	c.LoadDesktopUnread()
	thread := model.Thread{ID: 7, ServerID: "session", Agent: appmodel.AgentClaude, CompletedAtMs: 200, HistoryLoaded: true}
	c.ProjectCache["/background"] = []model.Thread{thread}
	key := appmodel.DesktopUnreadKey(thread)
	c.DesktopUnread[key] = 200
	oldHook := uiruntime.CurrentInvalidateHook()
	var updates int
	uiruntime.SetInvalidateHook(func() { updates++ })
	t.Cleanup(func() { uiruntime.SetInvalidateHook(oldHook) })
	conn, ex := serveTestPhone(t, c)
	request := remote.Request{Op: "read", Thread: 999, ServerID: thread.ServerID, Agent: appmodel.AgentClaude, Project: "/background"}
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
			if got := c.DesktopThreadUnread(thread); got != (version != 200) {
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
	restored := &controller.Controller{}
	restored.LoadDesktopUnread()
	if restored.DesktopThreadUnread(thread) {
		t.Fatal("the phone's read did not persist across desktop restarts")
	}
}

func TestPhoneCompletionReadUsesStableIdentity(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	thread := model.Thread{ID: 1, ServerID: "new-session", CompletedAtMs: 200}
	c.SetThreads([]model.Thread{thread})
	c.DesktopUnread[appmodel.DesktopUnreadKey(thread)] = 200
	for _, r := range []remote.Request{
		{Thread: 1, ServerID: "old-session", ReadCompletedAtMs: 200},
		{Thread: 1, ServerID: thread.ServerID, Agent: appmodel.AgentClaude, ReadCompletedAtMs: 200},
		{Thread: 1, ServerID: thread.ServerID, Project: "/wrong", ReadCompletedAtMs: 200},
	} {
		c.MarkPhoneCompletionRead(r)
		if !c.DesktopThreadUnread(thread) {
			t.Fatal("an acknowledgement for another conversation cleared unread")
		}
	}
}
