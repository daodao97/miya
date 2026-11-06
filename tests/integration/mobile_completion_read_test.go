package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/model"
	phoneserver "miya/internal/phone/server"
)

func TestMobileCompletionReadWaitsForLiveContents(t *testing.T) {
	m := testMobileApp()
	m.PushDisabled = true
	thread := model.Thread{ID: 1, ServerID: "s1", CompletedAtMs: 100, HistoryLoaded: true,
		Messages: []model.Message{{ID: 10, Role: "assistant", Content: "完成结果"}}}
	m.Open = thread.ID
	m.ShowThread(thread)
	if _, _, ok := m.CompletionReadRequest(); ok {
		t.Fatal("cached messages and sidebar metadata acknowledged unseen contents")
	}
	sync := phoneserver.NewThreadSync(nil)
	delta, _ := sync.Update(thread)
	m.ReceiveThread(thread.ID, delta)
	r, read, ok := m.CompletionReadRequest()
	if !ok || r.Op != "read" || r.ServerID != thread.ServerID || r.Project != "/a" || r.ReadCompletedAtMs != 100 || r.ReadVersion != 0 {
		t.Fatalf("completed contents need a read independent of push registration: %+v, %v", r, ok)
	}
	m.CompletionRead = read
	if _, _, ok := m.CompletionReadRequest(); ok {
		t.Fatal("the same completed result was acknowledged twice")
	}
	thread.CompletedAtMs = 200
	delta, _ = sync.Update(thread)
	m.ReceiveThread(thread.ID, delta)
	if r, _, ok := m.CompletionReadRequest(); !ok || r.ReadCompletedAtMs != 200 {
		t.Fatal("a metadata-only completion delta with unchanged messages was not acknowledged")
	}
	m.Subscribe(0)
	if _, _, ok := m.CompletionReadRequest(); ok {
		t.Fatal("reconnecting or changing subscriptions reused an earlier received result")
	}
}

func TestMobileCompletionReadRejectsIncompleteResults(t *testing.T) {
	for _, state := range []string{"running", "loading", "history-missing", "messages-missing", "not-completed", "other-thread"} {
		t.Run(state, func(t *testing.T) {
			m := testMobileApp()
			thread := model.Thread{ID: 1, ServerID: "s1", CompletedAtMs: 100, HistoryLoaded: true,
				Messages: []model.Message{{ID: 10, Content: "result"}}}
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
			m.Open = 1
			m.ShowThread(thread)
			m.CompletionReceived = appmodel.DesktopRead{Key: appmodel.DesktopUnreadKey(thread), Version: thread.CompletedAtMs}
			if _, _, ok := m.CompletionReadRequest(); ok {
				t.Fatal("acknowledged an unavailable completed result")
			}
		})
	}
}
