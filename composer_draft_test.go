package main

import "testing"

func TestSentDraftStaysEmptyWhenSwitchingConversations(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.claudeBinary = "/unused/claude"
	c.setThreads([]Thread{
		{ID: 1, Agent: agentClaude, Busy: true, HistoryLoaded: true},
		{ID: 2, Agent: agentClaude, HistoryLoaded: true, Draft: "另一条对话的草稿"},
	})
	c.selectThread(c.threadWithID(1))
	c.rememberDraft("已经发送的消息")
	stale := c.current()
	tt.Frame()
	c.send()
	if c.draft() != "" || len(c.current().Queue) != 1 {
		t.Fatal("message was not accepted into the queue")
	}
	c.selectThread(c.threadWithID(2))
	tt.Frame()
	if c.draft() != "另一条对话的草稿" {
		t.Fatal("switching lost an unsent draft")
	}
	// A sidebar callback can still hold the pre-send copy of the thread.
	c.selectThread(stale)
	tt.Frame()
	if c.draft() != "" || c.current().Draft != "" || inlineThreadRaw(app.complete.input.doc.Text()) != "" {
		t.Fatalf("sent text reappeared: draft=%q saved=%q", c.draft(), c.current().Draft)
	}
}

func TestUnsentAndReturnedDraftsSurviveConversationSwitches(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, HistoryLoaded: true}, {ID: 2, HistoryLoaded: true}})
	c.selectThread(c.threadWithID(1))
	c.rememberDraft("连接失败时保留的草稿")
	c.send() // Codex is offline, so this request is not accepted.
	c.selectThread(c.threadWithID(2))
	c.selectThread(c.threadWithID(1))
	if c.draft() != "连接失败时保留的草稿" {
		t.Fatal("an unaccepted request lost its draft")
	}
	c.rememberDraft("")
	c.restoreDraft(1, "退回的消息", []string{"/tmp/context.txt"})
	c.selectThread(c.threadWithID(2))
	c.selectThread(c.threadWithID(1))
	if c.draft() != "退回的消息" {
		t.Fatal("switching lost the returned draft")
	}
	c.restoreDraft(1, "过期的退回消息", nil)
	if c.draft() != "退回的消息" {
		t.Fatal("a late failure overwrote a newer draft")
	}
}
