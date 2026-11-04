package main

import "testing"

func TestMobileCompletionReadWaitsForLiveContents(t *testing.T) {
	m := testMobileApp()
	m.pushDisabled = true
	thread := Thread{ID: 1, ServerID: "s1", CompletedAtMs: 100, HistoryLoaded: true,
		Messages: []Message{{ID: 10, Role: "assistant", Content: "完成结果"}}}
	m.open = thread.ID
	m.showThread(thread)
	if _, _, ok := m.completionReadRequest(); ok {
		t.Fatal("cached messages and sidebar metadata acknowledged unseen contents")
	}
	sync := newPhoneThreadSync(nil)
	delta, _ := sync.update(thread)
	m.receiveThread(thread.ID, delta)
	r, read, ok := m.completionReadRequest()
	if !ok || r.Op != "read" || r.ServerID != thread.ServerID || r.Project != "/a" || r.ReadCompletedAtMs != 100 || r.ReadVersion != 0 {
		t.Fatalf("completed contents need a read independent of push registration: %+v, %v", r, ok)
	}
	m.completionRead = read
	if _, _, ok := m.completionReadRequest(); ok {
		t.Fatal("the same completed result was acknowledged twice")
	}
	thread.CompletedAtMs = 200
	delta, _ = sync.update(thread)
	m.receiveThread(thread.ID, delta)
	if r, _, ok := m.completionReadRequest(); !ok || r.ReadCompletedAtMs != 200 {
		t.Fatal("a metadata-only completion delta with unchanged messages was not acknowledged")
	}
	m.subscribe(0)
	if _, _, ok := m.completionReadRequest(); ok {
		t.Fatal("reconnecting or changing subscriptions reused an earlier received result")
	}
}

func TestMobileCompletionReadRejectsIncompleteResults(t *testing.T) {
	for _, state := range []string{"running", "loading", "history-missing", "messages-missing", "not-completed", "other-thread"} {
		t.Run(state, func(t *testing.T) {
			m := testMobileApp()
			thread := Thread{ID: 1, ServerID: "s1", CompletedAtMs: 100, HistoryLoaded: true,
				Messages: []Message{{ID: 10, Content: "result"}}}
			switch state {
			case "running":
				thread.Busy = true
			case "loading":
				thread.Loading = true
			case "history-missing":
				thread.HistoryLoaded = false
			case "messages-missing":
				thread.Messages = nil
			case "not-completed":
				thread.CompletedAtMs = 0
			case "other-thread":
				thread.ID = 2
			}
			m.open = 1
			m.showThread(thread)
			m.completionReceived = desktopRead{Key: desktopUnreadKey(thread), Version: thread.CompletedAtMs}
			if _, _, ok := m.completionReadRequest(); ok {
				t.Fatal("acknowledged an unavailable completed result")
			}
		})
	}
}
