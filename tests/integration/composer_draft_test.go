package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
	"miya/internal/controller"
)

func TestSentDraftStaysEmptyWhenSwitchingConversations(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.ClaudeBinary = "/unused/claude"
	c.SetThreads([]model.Thread{
		{ID: 1, Agent: appmodel.AgentClaude, Busy: true, HistoryLoaded: true},
		{ID: 2, Agent: appmodel.AgentClaude, HistoryLoaded: true, Draft: "另一条对话的草稿"},
	})
	c.SelectThread(c.ThreadWithID(1))
	c.RememberDraft("已经发送的消息")
	stale := c.Current()
	tt.Frame()
	c.Send()
	if c.Draft() != "" || len(c.Current().Queue) != 1 {
		t.Fatal("message was not accepted into the queue")
	}
	c.SelectThread(c.ThreadWithID(2))
	tt.Frame()
	if c.Draft() != "另一条对话的草稿" {
		t.Fatal("switching lost an unsent draft")
	}
	// A sidebar callback can still hold the pre-send copy of the thread.
	c.SelectThread(stale)
	tt.Frame()
	if c.Draft() != "" || c.Current().Draft != "" || conversation.InlineThreadRaw(app.Complete.Input.Doc.Text()) != "" {
		t.Fatalf("sent text reappeared: draft=%q saved=%q", c.Draft(), c.Current().Draft)
	}
}

func TestUnsentAndReturnedDraftsSurviveConversationSwitches(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, HistoryLoaded: true}, {ID: 2, HistoryLoaded: true}})
	c.SelectThread(c.ThreadWithID(1))
	c.RememberDraft("连接失败时保留的草稿")
	c.Send() // Codex is offline, so this request is not accepted.
	c.SelectThread(c.ThreadWithID(2))
	c.SelectThread(c.ThreadWithID(1))
	if c.Draft() != "连接失败时保留的草稿" {
		t.Fatal("an unaccepted request lost its draft")
	}
	c.RememberDraft("")
	c.RestoreDraft(1, "退回的消息", []string{"/tmp/context.txt"})
	c.SelectThread(c.ThreadWithID(2))
	c.SelectThread(c.ThreadWithID(1))
	if c.Draft() != "退回的消息" {
		t.Fatal("switching lost the returned draft")
	}
	c.RestoreDraft(1, "过期的退回消息", nil)
	if c.Draft() != "退回的消息" {
		t.Fatal("a late failure overwrote a newer draft")
	}
}
