package integration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	"miya/internal/notification"
)

func TestClaudeEmptyResultExplained(t *testing.T) {
	c := testController()
	c.ClaudeCommand = "claude --settings ~/proxy.json"
	c.SetThreads([]model.Thread{{ID: 1, Agent: "claude-code", ServerID: "s1", Messages: []model.Message{{ID: 1, Role: "user", Content: "继续"}}}})
	var f claude.Frame
	_ = json.Unmarshal([]byte(`{"type":"result","is_error":true,"errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null"]}`), &f)
	c.HandleClaude(1, f)
	msgs := c.Threads()[0].Messages
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "returned no response") || !strings.Contains(last, "proxy") || !strings.Contains(last, "stop_reason=null") {
		t.Fatalf("message %q", last)
	}
	// A real error after a reply keeps Claude Code's own words.
	if got := claude.ErrorMessage("rate limited", true, true); got != "rate limited" {
		t.Fatalf("rewrote %q", got)
	}
	if got := claude.ErrorMessage("x", false, false); strings.Contains(got, "custom launch command") {
		t.Fatalf("custom hint without a custom command: %q", got)
	}
}

func TestClaudeAssistantAPIErrorIsNotOutput(t *testing.T) {
	c := testController()
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, Busy: true, Messages: []model.Message{{Role: "user", Content: "同步上游"}}}})
	var f claude.Frame
	if err := json.Unmarshal([]byte(`{"type":"assistant","error":"api_error","message":{"id":"api-failure","content":[{"type":"text","text":"服务暂不可用"}]}}`), &f); err != nil {
		t.Fatal(err)
	}
	c.HandleClaude(1, f)
	th := c.ThreadWithID(1)
	if len(th.Messages) != 2 || th.Messages[1].Status != "failed" || msgfmt.FailedAfterOutput(th, 0) || claude.TurnHasOutput(th.Messages) {
		t.Fatalf("assistant API failure counted as completed work: %+v", th.Messages)
	}
	c.HandleClaude(1, claude.Frame{Type: "result", IsError: true, Errors: []string{"upstream unavailable"}})
	last := c.ThreadWithID(1).Messages[2]
	if last.Role != "error" || !strings.Contains(last.Content, "returned no response") {
		t.Fatalf("API failure reported as a partial reply: %+v", last)
	}
}

func TestClaudeStoppedResultDoesNotReportFailure(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
		tool        bool
	}{
		{name: "diagnostic before output", frame: `{"type":"result","is_error":true,"errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use"]}`},
		{name: "diagnostic during tool", tool: true, frame: `{"type":"result","is_error":true,"errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use"]}`},
		{name: "error result text", frame: `{"type":"result","is_error":true,"result":"Interrupted"}`},
		{name: "empty success", frame: `{"type":"result","result":""}`},
		{name: "final text after stop", frame: `{"type":"result","result":"Partial reply"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c := testController()
			c.Cwd = t.TempDir()
			c.ClaudeCommand = "claude --settings ~/proxy.json"
			messages := []model.Message{{Role: "user", Content: "继续"}}
			if tc.tool {
				messages = append(messages, model.Message{Role: "tool", Kind: "claudeTool", Content: "Bash", Status: "inProgress"})
			}
			c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, Busy: true, Messages: messages}})
			old := c.ThreadWithID(1)
			c.StopThread(1)
			var f claude.Frame
			if err := json.Unmarshal([]byte(tc.frame), &f); err != nil {
				t.Fatal(err)
			}
			c.HandleClaude(1, f)
			got := c.ThreadWithID(1)
			if got.Status != "Stopping…" || len(got.Messages) != len(messages) {
				t.Fatalf("stop result reported as output or failure: %+v", got)
			}
			if tc.tool && got.Messages[1].Status != "inProgress" {
				t.Fatalf("stop result changed tool to %q", got.Messages[1].Status)
			}
			c.FinishClaude(1, c.Cwd, errors.New("signal: killed"))
			got = c.ThreadWithID(1)
			if got.Busy || got.Status != "Stopped" || got.CompletedAtMs == 0 || len(got.Messages) != len(messages) {
				t.Fatalf("stop did not finish cleanly: %+v", got)
			}
			if tc.tool && got.Messages[1].Status != "interrupted" {
				t.Fatalf("stopped tool status = %q", got.Messages[1].Status)
			}
			if notice, ok := notification.Completion(old, got); ok {
				t.Fatalf("requested stop produced a completion notification: %+v", notice)
			}
		})
	}
}

func TestClaudeResultSurvivesProcessExit(t *testing.T) {
	for _, tc := range []struct {
		name, frame, wantRole, wantText string
		messages                        []model.Message
		stopped                         bool
	}{
		{name: "API failure", frame: `{"type":"result","is_error":true,"errors":["upstream unavailable"]}`, wantRole: "error", wantText: "upstream unavailable"},
		{name: "empty success", frame: `{"type":"result","subtype":"success","is_error":false,"result":""}`, wantRole: "error", wantText: "returned no response"},
		{name: "final text only", frame: `{"type":"result","result":"Hello"}`, wantRole: "assistant", wantText: "Hello"},
		{name: "streamed text", frame: `{"type":"result","result":"Hello"}`, messages: []model.Message{{Role: "assistant", Content: "Hello"}}, wantRole: "assistant", wantText: "Hello"},
		{name: "tool output", frame: `{"type":"result"}`, messages: []model.Message{{Role: "tool", Content: "done"}}, wantRole: "tool", wantText: "done"},
		{name: "scheduled idle", frame: `{"type":"result"}`, wantRole: "user", wantText: "继续"},
		{name: "stopped", frame: `{"type":"result"}`, stopped: true, wantRole: "user", wantText: "继续"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c := testController()
			c.Cwd = t.TempDir()
			c.ClaudeCommand = "claude --settings ~/proxy.json"
			messages := []model.Message{{Role: "error", Content: "old failure"}, {Role: "user", Content: "继续"}}
			messages = append(messages, tc.messages...)
			c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, Busy: true, StopRequested: tc.stopped, Messages: messages}})
			var f claude.Frame
			if err := json.Unmarshal([]byte(tc.frame), &f); err != nil {
				t.Fatal(err)
			}
			f.KeepingAlive = tc.name == "scheduled idle"
			c.HandleClaude(1, f)
			if !f.KeepingAlive {
				c.FinishClaude(1, c.Cwd, nil)
			}
			got := c.Threads()[0]
			last := got.Messages[len(got.Messages)-1]
			if last.Role != tc.wantRole || !strings.Contains(last.Content, tc.wantText) {
				t.Fatalf("last message = %+v", last)
			}
			if tc.wantRole == "error" {
				if got.Status != last.Content {
					t.Fatalf("process exit cleared API error: status = %q, message = %q", got.Status, last.Content)
				}
			} else if got.Status != "" && !tc.stopped {
				t.Fatalf("previous turn's error leaked into status: %q", got.Status)
			}
			wantLen := len(messages)
			if tc.wantRole == "error" || tc.name == "final text only" {
				wantLen++
			}
			if len(got.Messages) != wantLen || got.Busy {
				t.Fatalf("completion state: messages = %d, want %d; busy = %v", len(got.Messages), wantLen, got.Busy)
			}
		})
	}
}

func TestClaudeErrorVisibleAfterProcessExit(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, Busy: true, HistoryLoaded: true,
		Messages: []model.Message{{ID: c.NextID(), Role: "user", Content: "继续"}}}})
	c.SetActive(1)
	old := c.Current()
	c.HandleClaude(1, claude.Frame{Type: "result", IsError: true, Errors: []string{"upstream unavailable"}})
	c.FinishClaude(1, c.Cwd, nil)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "upstream unavailable") {
		t.Fatalf("error missing from conversation: %q", texts)
	}
	if _, ok := tt.Find("Retry response"); !ok {
		t.Fatalf("error has no retry action: %q", texts)
	}
	if notice, ok := notification.Completion(old, c.Current()); !ok || notice.Title != "Miya · Failed" {
		t.Fatalf("error reported as success: %+v, %v", notice, ok)
	}
}

func TestStalledHint(t *testing.T) {
	now := time.Now()
	base := model.Thread{Agent: "claude-code", Busy: true, LastOutputAtMs: now.Add(-90 * time.Second).UnixMilli()}
	if h := appmodel.StalledHint(base, false, now); !strings.Contains(h, "1m 30s") {
		t.Fatalf("hint %q", h)
	}
	fresh := base
	fresh.LastOutputAtMs = now.Add(-10 * time.Second).UnixMilli()
	tool := base
	tool.Messages = []model.Message{{Role: "tool", Status: "inProgress"}}
	idle := base
	idle.Busy = false
	codex := base
	codex.Agent = ""
	for name, th := range map[string]model.Thread{"recent output": fresh, "tool running": tool, "not busy": idle, "codex": codex} {
		if h := appmodel.StalledHint(th, false, now); h != "" {
			t.Errorf("%s: %q", name, h)
		}
	}
	if appmodel.StalledHint(base, true, now) != "" {
		t.Error("hint while an approval waits")
	}
}

func TestStalledHintShowsInConversation(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Agent: "claude-code", ServerID: "s1", Title: "Slow", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-2 * time.Minute).UnixMilli(), LastOutputAtMs: time.Now().Add(-2 * time.Minute).UnixMilli(),
		Messages: []model.Message{{ID: 1, Role: "user", Content: "继续"}}}})
	c.SetActive(1)
	tt.Frame()
	for _, s := range tt.Texts() {
		if strings.Contains(s, "No response for") {
			return
		}
	}
	t.Fatalf("no stall hint in %q", tt.Texts())
}
