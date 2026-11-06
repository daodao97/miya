package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/claude"
	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/uiruntime"
)

func TestClaudeMessagesReconcileAndStayOnTheirThread(t *testing.T) {
	c := testController()
	c.Active, c.SetActive = uiruntime.CreateSignal(1)
	c.Agent, c.SetAgent = uiruntime.CreateSignal("codex")
	c.SetThreads([]model.Thread{{ID: 1, Agent: "claude-code", ServerID: "s1"}, {ID: 2, ServerID: "codex1"}})
	emit := func(s string) {
		var f claude.Frame
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			t.Fatal(err)
		}
		c.HandleClaude(1, f)
	}
	emit(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`)
	emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}}`)
	emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Hello"},{"type":"tool_use","id":"tool1","name":"Read","input":{"file_path":"main.go"}}]}}`)
	emit(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","content":"file contents"}]}}`)
	messages := c.Current().Messages
	if len(messages) != 2 || messages[0].Content != "Hello" || messages[1].Status != "completed" {
		t.Fatalf("bad stream reconciliation: %+v", messages)
	}
	if len(c.Threads()[1].Messages) != 0 {
		t.Fatal("Claude events leaked into Codex")
	}
	c.SetAgent("codex")
	if c.CurrentAgent() != "claude-code" {
		t.Fatal("existing thread agent was changed")
	}
	c.SetActive(0)
	if c.CurrentAgent() != "codex" {
		t.Fatal("new draft agent not applied")
	}
}

func TestClaudeApprovalAndQuestions(t *testing.T) {
	c := testController()
	var f claude.Frame
	_ = json.Unmarshal([]byte(`{"type":"control_request","request_id":"a1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"pwd"}}}`), &f)
	c.HandleClaude(1, f)
	a := c.Approvals()[0]
	if a.Method != "claude/permission" || !strings.Contains(string(a.ID), "claude:1:a1") || a.ThreadID != "t1" {
		t.Fatal("approval lost routing identity")
	}
	if !c.RespondClaude(a, codex.ApprovalResult(a, false)) || len(c.Approvals()) != 0 {
		t.Fatal("stale Claude approval was sent to Codex")
	}
	_ = json.Unmarshal([]byte(`{"type":"control_request","request_id":"q1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"Pick?","header":"Choice","options":[{"label":"A","description":"a"}]}]}}}`), &f)
	c.HandleClaude(1, f)
	a = c.Approvals()[0]
	if a.Method != "item/tool/requestUserInput" || a.ThreadID != "t1" || len(a.Questions) != 1 || a.Questions[0].ID != "Pick?" || len(a.Questions[0].Options) != 1 {
		t.Fatal("question parsing failed")
	}
}
