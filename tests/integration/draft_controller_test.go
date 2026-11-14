package integration

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"testing"
)

func TestNSGoSentDraftStaysEmptyWhenSwitchingConversations(t *testing.T) {
	c := isolatedController(t)
	c.ClaudeBinary = "/unused/claude"
	c.SetThreads([]model.Thread{
		{ID: 1, Agent: appmodel.AgentClaude, Busy: true, HistoryLoaded: true},
		{ID: 2, Agent: appmodel.AgentClaude, HistoryLoaded: true, Draft: "另一条对话的草稿"},
	})
	c.SelectThread(c.ThreadWithID(1))
	c.RememberDraft("已经发送的消息")
	stale := c.Current()
	c.Send()
	if c.Draft() != "" || len(c.Current().Queue) != 1 {
		t.Fatal("message was not accepted into the queue")
	}
	c.SelectThread(c.ThreadWithID(2))
	if c.Draft() != "另一条对话的草稿" {
		t.Fatal("switching lost an unsent draft")
	}
	// A sidebar callback can still hold the pre-send copy of the thread.
	c.SelectThread(stale)
	if c.Draft() != "" || c.Current().Draft != "" {
		t.Fatalf("sent text reappeared: draft=%q saved=%q", c.Draft(), c.Current().Draft)
	}
}
