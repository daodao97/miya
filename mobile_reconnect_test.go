package main

import (
	appmodel "miya/internal/appmodel"
	remote "miya/internal/remote"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func TestMobileReconnectCountdownUpdatesOnTheFrameClock(t *testing.T) {
	m := testMobileApp()
	m.Connected = false
	m.Router().Push("/settings")
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	m.RetryAt = tt.Now().Add(15 * time.Second)
	m.RetryError = "网络暂时不可用"
	tt.Frame()
	for _, tc := range []struct {
		advance time.Duration
		label   string
	}{
		{0, "Disconnected. Retrying in 15 seconds.: 网络暂时不可用"},
		{time.Second, "Disconnected. Retrying in 14 seconds.: 网络暂时不可用"},
		{12 * time.Second, "Disconnected. Retrying in 2 seconds.: 网络暂时不可用"},
		{2 * time.Second, "Reconnecting to desktop…"},
	} {
		if tc.advance > 0 {
			tt.Advance(tc.advance)
		}
		if !tt.HasText(tc.label) {
			t.Fatalf("countdown did not advance: want %q, got %q", tc.label, tt.Texts())
		}
	}
	m.Disconnect()
	tt.Frame()
	if !m.RetryAt.IsZero() || m.RetryError != "" || m.ConnectionLine() != "Disconnected" {
		t.Fatal("disconnect retained a stale countdown")
	}
}

func TestMobileSendKeepsMessagesDuringHistoryReload(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		m := testMobileApp()
		m.OpenThread(m.State.Projects[0].Threads[0])
		messages := []appmodel.Message{{ID: 10, Role: "assistant", Content: "Previous reply"}}
		m.Remember("s1", messages)
		m.ShowThread(appmodel.Thread{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: messages})
		tt := ui.NewTester(m.ViewRoot, 390, 750)
		tt.Frame()
		m.Sending, m.Draft = true, "Follow-up"
		m.Sent(1, "")
		// Older desktops can send an empty loading delta, even with the
		// history-loaded flag left over from a previous read.
		m.ReceiveThread(1, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 1, ServerID: "s1", Loading: true, HistoryLoaded: loaded}, Reset: true})
		tt.Frame()
		if !tt.HasText("Previous reply") || tt.HasText("Loading conversation…") {
			t.Fatalf("sending hid the history (loaded %v): %q", loaded, tt.Texts())
		}
		if got := m.CachedMessages("s1"); !sameMessages(got, messages) {
			t.Fatal("loading erased the phone's message cache")
		}
		m.ReceiveThread(1, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 1, ServerID: "s1", HistoryLoaded: true},
			Order: []int{10, 11}, Changed: []appmodel.Message{{ID: 11, Role: "user", Content: "Follow-up"}}})
		tt.Frame()
		if !tt.HasText("Previous reply") || !tt.HasText("Follow-up") {
			t.Fatalf("the follow-up did not extend the visible history: %q", tt.Texts())
		}
	}
}

func TestMobileRebindsOpenThreadAfterDesktopRestart(t *testing.T) {
	m := testMobileApp()
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.ShowThread(appmodel.Thread{ID: 1, ServerID: "s1", Title: "Original", HistoryLoaded: true,
		Messages: []appmodel.Message{{ID: 10, Role: "assistant", Content: "Cached reply"}}})
	m.Draft, m.Images = "Unsent follow-up", []string{"photo.png"}
	// The old number was reused for another conversation in this process.
	s := appmodel.PhoneState{Projects: []appmodel.PhoneProject{{Path: "/a", Threads: []appmodel.Thread{
		{ID: 1, ServerID: "different", Title: "Wrong conversation"},
		{ID: 77, ServerID: "s1", Title: "Original"},
	}}}}
	m.ApplyPhoneState(s)
	cur := m.Viewer.Current()
	if m.Open != 77 || cur.ID != 77 || cur.ServerID != "s1" || len(cur.Messages) != 1 || cur.Messages[0].Content != "Cached reply" {
		t.Fatalf("reconnect lost or changed the open conversation: open %d, %+v", m.Open, cur)
	}
	if m.Draft != "Unsent follow-up" || len(m.Images) != 1 || m.Router().Path() != "/thread" {
		t.Fatal("reconnect lost the draft, attachment, or open page")
	}
	for _, op := range []string{"subscribe", "send", "stop"} {
		r := m.ThreadRequest(op, m.Open)
		if r.Thread != 77 || r.ServerID != "s1" || r.Agent != appmodel.AgentCodex || r.Project != "/a" {
			t.Fatalf("%s targets the wrong conversation: %+v", op, r)
		}
	}
	// A delayed frame for the old number must not replace the rebound page.
	m.ReceiveThread(1, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 1, ServerID: "different"}, Reset: true})
	if cur := m.Viewer.Current(); cur.ServerID != "s1" || len(cur.Messages) != 1 {
		t.Fatal("a stale frame replaced the open conversation")
	}
}

func TestMobileReconnectKeepsIdentityBeforeListArrives(t *testing.T) {
	m := testMobileApp()
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.Remember("s1", []appmodel.Message{{ID: 10, Content: "Cached reply"}})
	// A new/partial list may already associate the old number with another
	// conversation. Subscription and send must still identify the open page.
	m.State = appmodel.PhoneState{Projects: []appmodel.PhoneProject{{Path: "/wrong", Threads: []appmodel.Thread{{ID: 1, ServerID: "wrong"}}}}}
	r := m.SubscribeRequest(1)
	if r.ServerID != "s1" || r.Project != "" || len(r.Have) != 1 {
		t.Fatalf("reconnect subscribed using the wrong cached number: %+v", r)
	}
	// Both old and new desktops can temporarily report a missing thread.
	for _, sid := range []string{"", "s1"} {
		m.ReceiveThread(1, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 1, ServerID: sid, Status: "Conversation is no longer on the desktop"}, Reset: true})
		if cur := m.Viewer.Current(); cur.ServerID != "s1" {
			t.Fatalf("missing-thread frame erased the stable identity: %+v", cur)
		}
	}
	m.ApplyPhoneState(appmodel.PhoneState{Projects: []appmodel.PhoneProject{{Path: "/a", Threads: []appmodel.Thread{{ID: 8, ServerID: "s1"}}}}})
	if m.Open != 8 || m.Viewer.Current().ServerID != "s1" {
		t.Fatal("a later startup list did not repair the open page")
	}
}

func TestMobileRejectsReusedIDFromStaleDesktopFrame(t *testing.T) {
	m := testMobileApp()
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.ReceiveThread(1, appmodel.PhoneThreadDelta{Thread: appmodel.Thread{ID: 1, ServerID: "other"}, Reset: true,
		Changed: []appmodel.Message{{ID: 2, Content: "Wrong reply"}}, Order: []int{2}})
	if m.Viewer.Current().ServerID != "s1" || len(m.Viewer.Current().Messages) != 0 {
		t.Fatal("reused numeric ID exposed another conversation")
	}
}

func TestPhoneStableIdentityOverridesNumericID(t *testing.T) {
	c := testController()
	c.Cwd = "/a"
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "wrong"}, {ID: 8, ServerID: "original"}})
	c.ProjectCache = map[string][]appmodel.Thread{"/b": {{ID: 9, Agent: appmodel.AgentClaude, ServerID: "claude"}}}
	for _, tc := range []struct {
		r    remote.Request
		want int
	}{
		{remote.Request{Thread: 1, ServerID: "original", Agent: appmodel.AgentCodex}, 8},
		{remote.Request{Thread: 1, ServerID: "claude", Agent: appmodel.AgentClaude, Project: "/b"}, 9},
		{remote.Request{Thread: 1, ServerID: "deleted"}, 0},
		{remote.Request{Thread: 1, ServerID: "original", Agent: appmodel.AgentClaude}, 0},
		{remote.Request{Thread: 1, ServerID: "original", Project: "/wrong"}, 0},
		{remote.Request{Thread: 1}, 1}, // older phones remain compatible
	} {
		got := c.PhoneThreadFor(tc.r)
		if got.Thread.ID != tc.want || got.Found != (tc.want != 0) {
			t.Fatalf("resolved %+v to %+v, want ID %d", tc.r, got, tc.want)
		}
	}
}
