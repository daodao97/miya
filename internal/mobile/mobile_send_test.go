package mobile

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/uiruntime"
)

func TestSendButtonShowsMessageBeforeAsyncImageFailure(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	m := NewMobileApp()
	m.Loaded, m.Link, m.Connected = true, "miya://test", true
	m.State = phoneproto.State{Projects: []model.ProjectSnapshot{{Name: "Project", Path: "/project", Current: true}}}
	m.Router().Transition = ui.TransitionNone
	th := model.Thread{ID: 1, ServerID: "one", Agent: "codex", HistoryLoaded: true}
	m.OpenThread(th)
	m.ShowThread(th)
	// Reading the missing attachment fails before the transport is reached.
	m.Conn = new(remote.Conn)
	m.Draft, m.Images = "立即出现在对话里", []string{filepath.Join(t.TempDir(), "missing.png")}
	posts := make(chan func(), 1)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { posts <- f }
	defer func() { uiruntime.RunOnMain = old }()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if len(m.Outbox) != 1 || m.Draft != "" || len(m.Images) != 0 || !tt.HasText("立即出现在对话里") || !tt.HasText("Uploading images…") {
		t.Fatalf("send button did not immediately commit the local message: %v", tt.Texts())
	}
	m.Draft = "下一条草稿"
	select {
	case f := <-posts:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("image failure did not return")
	}
	tt.Frame()
	if m.Sending || m.Draft != "下一条草稿" || !tt.HasText("立即出现在对话里") || !tt.HasText("Retry send") {
		t.Fatal("failed upload removed the message or next draft")
	}
	m.Conn = nil
	if err := tt.Click("Retry send"); err != nil {
		t.Fatal(err)
	}
	if m.Sending || len(m.Outbox) != 1 || !tt.HasText("Not connected to desktop") {
		t.Fatal("offline retry lost the pending message")
	}
	m.Draft = ""
	tt.Frame()
	if err := tt.Click("Edit message"); err != nil {
		t.Fatal(err)
	}
	if len(m.Outbox) != 0 || m.Draft != "立即出现在对话里" || len(m.Images) != 1 {
		t.Fatal("editing a failed message lost text or its attachment")
	}
}

func TestDisconnectKeepsPendingMessageAndLateEchoConfirmsIt(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	m := NewMobileApp()
	m.Loaded, m.Link, m.Connected = true, "miya://test", true
	th := model.Thread{ID: 1, ServerID: "one", Agent: "codex", HistoryLoaded: true}
	m.Open = th.ID
	m.ShowThread(th)
	m.BeginSend(remote.Request{Op: "send", Thread: th.ID, ServerID: th.ServerID, Agent: th.Agent, Text: "断线期间保留"}, nil)
	m.Draft = "另一个草稿"
	m.DisconnectDesktop(false)
	if m.Sending || len(m.Outbox) != 1 || m.Outbox[0].failure == "" || m.Draft != "另一个草稿" {
		t.Fatal("disconnect discarded the pending message")
	}
	th.Messages = []model.Message{{ID: 7, Role: "user", Content: "断线期间保留"}}
	m.ReceiveThread(th.ID, phoneproto.ThreadDelta{Thread: th, Reset: true, Order: []int{7}, Changed: th.Messages})
	if len(m.Outbox) != 0 || m.Draft != "另一个草稿" {
		t.Fatal("late delivery was not reconciled after disconnect")
	}
}
