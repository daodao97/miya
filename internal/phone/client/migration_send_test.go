package client

import (
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"testing"
)

func TestNSGoDisconnectKeepsPendingMessageAndLateEchoConfirmsIt(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	m := New()
	m.Loaded, m.Link, m.Connected = true, "miya://test", true
	th := model.Thread{ID: 1, ServerID: "one", Agent: "codex", HistoryLoaded: true}
	m.Open = th.ID
	m.ShowThread(th)
	m.BeginSend(remote.Request{Op: "send", Thread: th.ID, ServerID: th.ServerID, Agent: th.Agent, Text: "断线期间保留"}, nil)
	m.Draft = "另一个草稿"
	m.Disconnect()
	if m.Sending || len(m.Outbox) != 1 || m.Outbox[0].failure == "" || m.Draft != "另一个草稿" {
		t.Fatal("disconnect discarded the pending message")
	}
	th.Messages = []model.Message{{ID: 7, Role: "user", Content: "断线期间保留"}}
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{7}, Changed: th.Messages})
	if len(m.Outbox) != 0 || m.Draft != "另一个草稿" {
		t.Fatal("late delivery was not reconciled after disconnect")
	}
}
