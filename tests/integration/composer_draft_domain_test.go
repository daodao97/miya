package integration

import (
	"miya/internal/chat/model"
	"testing"
)

func TestUnsentAndReturnedDraftsSurviveConversationSwitches(t *testing.T) {
	c := isolatedController(t)
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
