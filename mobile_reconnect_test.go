package main

import (
	"testing"
	"time"

	"miya/internal/remote"

	"github.com/egoist/mygo/ui"
)

func TestMobileReconnectCountdownUpdatesOnTheFrameClock(t *testing.T) {
	m := testMobileApp()
	m.connected = false
	m.router().Push("/settings")
	tt := ui.NewTester(m.viewRoot, 390, 750)
	m.retryAt = tt.Now().Add(15 * time.Second)
	m.retryError = "网络暂时不可用"
	tt.Frame()
	for _, tc := range []struct {
		advance time.Duration
		label   string
	}{
		{0, "连接已断开，15 秒后重试：网络暂时不可用"},
		{time.Second, "连接已断开，14 秒后重试：网络暂时不可用"},
		{12 * time.Second, "连接已断开，2 秒后重试：网络暂时不可用"},
		{2 * time.Second, "正在重新连接桌面端…"},
	} {
		if tc.advance > 0 {
			tt.Advance(tc.advance)
		}
		if !tt.HasText(tc.label) {
			t.Fatalf("countdown did not advance: want %q, got %q", tc.label, tt.Texts())
		}
	}
	m.disconnect()
	tt.Frame()
	if !m.retryAt.IsZero() || m.retryError != "" || m.connectionLine() != "未连接" {
		t.Fatal("disconnect retained a stale countdown")
	}
}

func TestMobileSendKeepsMessagesDuringHistoryReload(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		m := testMobileApp()
		m.openThread(m.state.Projects[0].Threads[0])
		messages := []Message{{ID: 10, Role: "assistant", Content: "Previous reply"}}
		m.remember("s1", messages)
		m.showThread(Thread{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: messages})
		tt := ui.NewTester(m.viewRoot, 390, 750)
		tt.Frame()
		m.sending, m.draft = true, "Follow-up"
		m.sent(1, "")
		// Older desktops can send an empty loading delta, even with the
		// history-loaded flag left over from a previous read.
		m.receiveThread(1, phoneThreadDelta{Thread: Thread{ID: 1, ServerID: "s1", Loading: true, HistoryLoaded: loaded}, Reset: true})
		tt.Frame()
		if !tt.HasText("Previous reply") || tt.HasText("Loading conversation…") {
			t.Fatalf("sending hid the history (loaded %v): %q", loaded, tt.Texts())
		}
		if got := m.cachedMessages("s1"); !sameMessages(got, messages) {
			t.Fatal("loading erased the phone's message cache")
		}
		m.receiveThread(1, phoneThreadDelta{Thread: Thread{ID: 1, ServerID: "s1", HistoryLoaded: true},
			Order: []int{10, 11}, Changed: []Message{{ID: 11, Role: "user", Content: "Follow-up"}}})
		tt.Frame()
		if !tt.HasText("Previous reply") || !tt.HasText("Follow-up") {
			t.Fatalf("the follow-up did not extend the visible history: %q", tt.Texts())
		}
	}
}

func TestMobileRebindsOpenThreadAfterDesktopRestart(t *testing.T) {
	m := testMobileApp()
	m.openThread(m.state.Projects[0].Threads[0])
	m.showThread(Thread{ID: 1, ServerID: "s1", Title: "Original", HistoryLoaded: true,
		Messages: []Message{{ID: 10, Role: "assistant", Content: "Cached reply"}}})
	m.draft, m.images = "Unsent follow-up", []string{"photo.png"}
	// The old number was reused for another conversation in this process.
	s := phoneState{Projects: []phoneProject{{Path: "/a", Threads: []Thread{
		{ID: 1, ServerID: "different", Title: "Wrong conversation"},
		{ID: 77, ServerID: "s1", Title: "Original"},
	}}}}
	m.applyPhoneState(s)
	cur := m.view.c.current()
	if m.open != 77 || cur.ID != 77 || cur.ServerID != "s1" || len(cur.Messages) != 1 || cur.Messages[0].Content != "Cached reply" {
		t.Fatalf("reconnect lost or changed the open conversation: open %d, %+v", m.open, cur)
	}
	if m.draft != "Unsent follow-up" || len(m.images) != 1 || m.router().Path() != "/thread" {
		t.Fatal("reconnect lost the draft, attachment, or open page")
	}
	for _, op := range []string{"subscribe", "send", "stop"} {
		r := m.threadRequest(op, m.open)
		if r.Thread != 77 || r.ServerID != "s1" || r.Agent != agentCodex || r.Project != "/a" {
			t.Fatalf("%s targets the wrong conversation: %+v", op, r)
		}
	}
	// A delayed frame for the old number must not replace the rebound page.
	m.receiveThread(1, phoneThreadDelta{Thread: Thread{ID: 1, ServerID: "different"}, Reset: true})
	if cur := m.view.c.current(); cur.ServerID != "s1" || len(cur.Messages) != 1 {
		t.Fatal("a stale frame replaced the open conversation")
	}
}

func TestMobileReconnectKeepsIdentityBeforeListArrives(t *testing.T) {
	m := testMobileApp()
	m.openThread(m.state.Projects[0].Threads[0])
	m.remember("s1", []Message{{ID: 10, Content: "Cached reply"}})
	// A new/partial list may already associate the old number with another
	// conversation. Subscription and send must still identify the open page.
	m.state = phoneState{Projects: []phoneProject{{Path: "/wrong", Threads: []Thread{{ID: 1, ServerID: "wrong"}}}}}
	r := m.subscribeRequest(1)
	if r.ServerID != "s1" || r.Project != "" || len(r.Have) != 1 {
		t.Fatalf("reconnect subscribed using the wrong cached number: %+v", r)
	}
	// Both old and new desktops can temporarily report a missing thread.
	for _, sid := range []string{"", "s1"} {
		m.receiveThread(1, phoneThreadDelta{Thread: Thread{ID: 1, ServerID: sid, Status: "对话已不在桌面端"}, Reset: true})
		if cur := m.view.c.current(); cur.ServerID != "s1" {
			t.Fatalf("missing-thread frame erased the stable identity: %+v", cur)
		}
	}
	m.applyPhoneState(phoneState{Projects: []phoneProject{{Path: "/a", Threads: []Thread{{ID: 8, ServerID: "s1"}}}}})
	if m.open != 8 || m.view.c.current().ServerID != "s1" {
		t.Fatal("a later startup list did not repair the open page")
	}
}

func TestMobileRejectsReusedIDFromStaleDesktopFrame(t *testing.T) {
	m := testMobileApp()
	m.openThread(m.state.Projects[0].Threads[0])
	m.receiveThread(1, phoneThreadDelta{Thread: Thread{ID: 1, ServerID: "other"}, Reset: true,
		Changed: []Message{{ID: 2, Content: "Wrong reply"}}, Order: []int{2}})
	if m.view.c.current().ServerID != "s1" || len(m.view.c.current().Messages) != 0 {
		t.Fatal("reused numeric ID exposed another conversation")
	}
}

func TestPhoneStableIdentityOverridesNumericID(t *testing.T) {
	c := testController()
	c.cwd = "/a"
	c.setThreads([]Thread{{ID: 1, ServerID: "wrong"}, {ID: 8, ServerID: "original"}})
	c.projectCache = map[string][]Thread{"/b": {{ID: 9, Agent: agentClaude, ServerID: "claude"}}}
	for _, tc := range []struct {
		r    remote.Request
		want int
	}{
		{remote.Request{Thread: 1, ServerID: "original", Agent: agentCodex}, 8},
		{remote.Request{Thread: 1, ServerID: "claude", Agent: agentClaude, Project: "/b"}, 9},
		{remote.Request{Thread: 1, ServerID: "deleted"}, 0},
		{remote.Request{Thread: 1, ServerID: "original", Agent: agentClaude}, 0},
		{remote.Request{Thread: 1, ServerID: "original", Project: "/wrong"}, 0},
		{remote.Request{Thread: 1}, 1}, // older phones remain compatible
	} {
		got := c.phoneThreadFor(tc.r)
		if got.thread.ID != tc.want || got.found != (tc.want != 0) {
			t.Fatalf("resolved %+v to %+v, want ID %d", tc.r, got, tc.want)
		}
	}
}
