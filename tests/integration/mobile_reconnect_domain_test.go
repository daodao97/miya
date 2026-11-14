//go:build !mygo_ui

package integration

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"testing"
)

func TestMobileRebindsOpenThreadAfterDesktopRestart(t *testing.T) {
	m := testPhoneClient()
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.ShowThread(model.Thread{ID: 1, ServerID: "s1", Title: "Original", HistoryLoaded: true,
		Messages: []model.Message{{ID: 10, Role: "assistant", Content: "Cached reply"}}})
	m.Draft, m.Images = "Unsent follow-up", []string{"photo.png"}
	// The old number was reused for another conversation in this process.
	s := phoneproto.State{Projects: []model.ProjectSnapshot{{Path: "/a", Threads: []model.Thread{
		{ID: 1, ServerID: "different", Title: "Wrong conversation"},
		{ID: 77, ServerID: "s1", Title: "Original"},
	}}}}
	m.ApplyPhoneState(s)
	cur := m.Viewer.Current()
	if m.Open != 77 || cur.ID != 77 || cur.ServerID != "s1" || len(cur.Messages) != 1 || cur.Messages[0].Content != "Cached reply" {
		t.Fatalf("reconnect lost or changed the open conversation: open %d, %+v", m.Open, cur)
	}
	if m.Draft != "Unsent follow-up" || len(m.Images) != 1 || m.Path != "/thread" {
		t.Fatal("reconnect lost the draft, attachment, or open page")
	}
	for _, op := range []string{"subscribe", "send", "stop"} {
		r := m.ThreadRequest(op, m.Open)
		if r.Thread != 77 || r.ServerID != "s1" || r.Agent != appmodel.AgentCodex || r.Project != "/a" {
			t.Fatalf("%s targets the wrong conversation: %+v", op, r)
		}
	}
	// A delayed frame for the old number must not replace the rebound page.
	m.ReceiveThread(1, phoneproto.ThreadDelta{Thread: model.Thread{ID: 1, ServerID: "different"}, Reset: true})
	if cur := m.Viewer.Current(); cur.ServerID != "s1" || len(cur.Messages) != 1 {
		t.Fatal("a stale frame replaced the open conversation")
	}
}

func TestMobileReconnectKeepsIdentityBeforeListArrives(t *testing.T) {
	m := testPhoneClient()
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.Remember("s1", []model.Message{{ID: 10, Content: "Cached reply"}})
	// A new/partial list may already associate the old number with another
	// conversation. Subscription and send must still identify the open page.
	m.State = phoneproto.State{Projects: []model.ProjectSnapshot{{Path: "/wrong", Threads: []model.Thread{{ID: 1, ServerID: "wrong"}}}}}
	r := m.SubscribeRequest(1)
	if r.ServerID != "s1" || r.Project != "" || len(r.Have) != 1 {
		t.Fatalf("reconnect subscribed using the wrong cached number: %+v", r)
	}
	// Both old and new desktops can temporarily report a missing thread.
	for _, sid := range []string{"", "s1"} {
		m.ReceiveThread(1, phoneproto.ThreadDelta{Thread: model.Thread{ID: 1, ServerID: sid, Status: "Conversation is no longer on the desktop"}, Reset: true})
		if cur := m.Viewer.Current(); cur.ServerID != "s1" {
			t.Fatalf("missing-thread frame erased the stable identity: %+v", cur)
		}
	}
	m.ApplyPhoneState(phoneproto.State{Projects: []model.ProjectSnapshot{{Path: "/a", Threads: []model.Thread{{ID: 8, ServerID: "s1"}}}}})
	if m.Open != 8 || m.Viewer.Current().ServerID != "s1" {
		t.Fatal("a later startup list did not repair the open page")
	}
}

func TestMobileRejectsReusedIDFromStaleDesktopFrame(t *testing.T) {
	m := testPhoneClient()
	m.OpenThread(m.State.Projects[0].Threads[0])
	m.ReceiveThread(1, phoneproto.ThreadDelta{Thread: model.Thread{ID: 1, ServerID: "other"}, Reset: true,
		Changed: []model.Message{{ID: 2, Content: "Wrong reply"}}, Order: []int{2}})
	if m.Viewer.Current().ServerID != "s1" || len(m.Viewer.Current().Messages) != 0 {
		t.Fatal("reused numeric ID exposed another conversation")
	}
}

func TestPhoneStableIdentityOverridesNumericID(t *testing.T) {
	c := testController()
	c.Cwd = "/a"
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "wrong"}, {ID: 8, ServerID: "original"}})
	c.ProjectCache = map[string][]model.Thread{"/b": {{ID: 9, Agent: appmodel.AgentClaude, ServerID: "claude"}}}
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
