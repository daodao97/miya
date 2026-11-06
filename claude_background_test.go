package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"miya/internal/claude"
	"miya/internal/subagent"

	"github.com/egoist/mygo/ui"
)

func TestClaudeAutomaticAsyncAcknowledgementStaysRunning(t *testing.T) {
	c, emit := claudeTestThread(t)
	c.cwd = t.TempDir()
	c.update(1, func(th *Thread) { th.Busy = true; th.StartedAtMs = 1000 })
	emit(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"Agent","input":{"description":"Review diff"}}]}}`)
	emit(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"Launched"}]},"tool_use_result":{"isAsync":true,"agentId":"agent1"}}`)
	a := c.current().Messages[0].Agents[0]
	if a.ID != "agent1" || a.Status != subagent.Running {
		t.Fatalf("async acknowledgement marked complete: %+v", a)
	}
	c.handleClaude(1, claude.Frame{Type: "result", Result: "Review in background", KeepingAlive: true, WaitingForAgents: true})
	th := c.current()
	if !th.Busy || th.CompletedAtMs != 0 {
		t.Fatalf("pending agent ended the turn: busy=%v end=%d", th.Busy, th.CompletedAtMs)
	}
	c.handleClaude(1, claude.Frame{Type: "command_lifecycle", CommandState: "started"})
	if c.current().StartedAtMs != 1000 {
		t.Fatal("background continuation reset turn duration")
	}
	emit(`{"type":"system","subtype":"task_notification","task_id":"agent1","status":"completed","summary":"No issues"}`)
	if a := c.current().Messages[0].Agents[0]; a.Status != subagent.Completed {
		t.Fatalf("completion lost: %+v", a)
	}
	c.finishClaude(1, c.cwd, nil)
	if c.current().Busy || c.current().Messages[0].Agents[0].Status != subagent.Completed {
		t.Fatal("completion state changed on process exit")
	}
}

func TestClaudePendingAgentsInterruptedOnProcessExit(t *testing.T) {
	c, emit := claudeTestThread(t)
	c.cwd = t.TempDir()
	emit(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"Agent","input":{"description":"Review"}}]}}`)
	c.finishClaude(1, c.cwd, nil)
	if a := c.current().Messages[0].Agents[0]; a.Status != subagent.Interrupted {
		t.Fatalf("exited task still running or falsely completed: %+v", a)
	}
}

func TestClaudeBackgroundHistoryRequiresCompletionEvidence(t *testing.T) {
	for _, terminal := range []string{"none", "system", "native-message"} {
		t.Run(terminal, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			lines := []string{
				`{"type":"user","uuid":"u1","message":{"content":"Review"}}`,
				`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"tool_use","id":"call1","name":"Agent","input":{"description":"Review"}}]}}`,
				`{"type":"user","uuid":"r1","parentUuid":"a1","toolUseResult":{"isAsync":true,"agentId":"agent1"},"message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"Async agent launched successfully.\nagentId: agent1"}]}}`,
			}
			switch terminal {
			case "system":
				lines = append(lines, `{"type":"system","uuid":"n1","parentUuid":"r1","subtype":"task_notification","task_id":"agent1","status":"completed","summary":"Review complete"}`)
			case "native-message":
				lines = append(lines, `{"type":"user","uuid":"n1","parentUuid":"r1","isMeta":true,"message":{"content":"<task-notification><task-id>agent1</task-id><status>completed</status><summary>Review complete</summary></task-notification>"}}`)
			}
			path := writeClaudeSession(t, "/work", terminal, lines...)
			messages, err := parseClaudeSession(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 2 || len(messages[1].Agents) != 1 {
				t.Fatalf("notification leaked or task missing: %+v", messages)
			}
			want := subagent.Completed
			if terminal == "none" {
				want = subagent.Unconfirmed
			}
			if a := messages[1].Agents[0]; a.ID != "agent1" || a.Status != want {
				t.Fatalf("history task = %+v, want %s", a, want)
			}
		})
	}
}

func TestClaudeSlashCommandDisplayAndEdit(t *testing.T) {
	args := "不改变功能的基础上简化代码\n" + strings.Repeat("完整保留参数", 25)
	raw := "<command-message>simplify</command-message> <command-name>/simplify</command-name> <command-args>" + args + "</command-args>"
	want := "/simplify " + args
	if got := claudeCommandText(raw); got != want {
		t.Fatalf("command shortened or malformed: %q", got)
	}
	for _, literal := range []string{"示例：" + raw, "```xml\n" + raw + "\n```", "`" + raw + "`"} {
		if claudeCommandText(literal) != literal {
			t.Fatal("rewrote literal example")
		}
	}
	app, _ := testApp(t)
	m := Message{ID: 1, Role: "user", Content: raw}
	for _, mobile := range []bool{false, true} {
		app.mobile = mobile
		tt := ui.NewTester(func(c *ui.Context) { app.messageView(c, m, false) }, 390, 560)
		tt.Frame()
		text := strings.Join(tt.Texts(), "\n")
		if strings.Contains(text, "<command-") || !strings.Contains(text, "/simplify") || !strings.Contains(text, "不改变功能") {
			t.Fatalf("raw command display on mobile=%v: %q", mobile, text)
		}
		automationSnapshot(t, tt, map[bool]string{false: "claude-command-desktop", true: "claude-command-mobile"}[mobile])
	}
	app.startEdit(m)
	if app.editText != want || m.Content != raw {
		t.Fatal("editing loses args or display changes source")
	}
}

func TestClaudeBackgroundActivityCountsAndFooter(t *testing.T) {
	app, _ := testApp(t)
	applyDesktopPalette(false)
	t.Cleanup(func() { applyDesktopPalette(false) })
	c := app.c
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Busy: true, StartedAtMs: time.Now().Add(-10 * time.Second).UnixMilli(), HistoryLoaded: true, Messages: []Message{{ID: c.nextID(), Role: "user", Content: "<command-message>simplify</command-message><command-name>/simplify</command-name><command-args>不改变功能的基础上简化代码</command-args>"}}}})
	c.setActive(1)
	for _, raw := range []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"Agent","input":{"description":"Review diff for reuse"}},{"type":"tool_use","id":"call2","name":"Agent","input":{"description":"Review diff for simplification"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"Async agent launched successfully.\nagentId: agent1"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call2","content":"Async agent launched successfully.\nagentId: agent2"}]}}`,
	} {
		var f claude.Frame
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			t.Fatal(err)
		}
		c.handleClaude(1, f)
	}
	c.handleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, WaitingForAgents: true, Result: "审查 agent 已在后台运行，完成后会汇总结果。"})
	tt := ui.NewTester(func(ctx *ui.Context) {
		for _, g := range threadMessageGroups(c.current(), groupMessages(c.current().Messages)) {
			app.groupView(ctx.Key(g.ID), g)
		}
	}, 744, 500)
	tt.Frame()
	text := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(text, "2 activities · 2 Running") || strings.Contains(text, "Worked for") || strings.Contains(text, "2 Completed") {
		t.Fatalf("pending task UI is wrong: %s", text)
	}
	automationSnapshot(t, tt, "claude-background-running")
	for _, id := range []string{"agent1", "agent2"} {
		c.handleClaude(1, claude.Frame{Type: "system", Subtype: "task_notification", TaskID: id, Status: json.RawMessage(`"completed"`), Summary: "Review complete"})
	}
	c.finishClaude(1, c.cwd, nil)
	tt.Frame()
	text = strings.Join(tt.Texts(), "\n")
	if !strings.Contains(text, "2 activities · 2 Completed") || !strings.Contains(text, "Worked for") {
		t.Fatalf("completed task UI is wrong: %s", text)
	}
	automationSnapshot(t, tt, "claude-background-completed")
}

// Real CLI verification is opt-in: one synthetic task in an empty workspace.
func TestClaudeLiveBackgroundCompletion(t *testing.T) {
	if os.Getenv("MIYA_CLAUDE_BACKGROUND_LIVE") != "1" {
		t.Skip("set MIYA_CLAUDE_BACKGROUND_LIVE=1")
	}
	command, err := claude.ParseCommand(claude.LoadCommand())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tools := "Agent,Bash"
	args := append(append([]string(nil), command.Args...), "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`, "--no-chrome")
	prompt := "Lifecycle verification in an empty disposable workspace. Start exactly ONE background Agent (run_in_background true if available), description Miya lifecycle verification, with this exact prompt: Run Bash with the exact command `sleep 2 && printf MIYA_BG_OK`, then return MIYA_BG_OK. Do not use any other tools or access files/network. After launching immediately reply MIYA_WAITING without waiting or using more tools. When its completion notification arrives, reply MIYA_BG_DONE."
	frames, done := make(chan claude.Frame, 256), make(chan error, 1)
	c, err := claude.Start(ctx, claude.Options{Binary: command.Path, Args: args, Env: command.Env, Cwd: t.TempDir(), SessionID: claude.NewSessionID(), PermissionMode: "default", Tools: &tools, Content: []map[string]any{{"type": "text", "text": prompt}}}, func(f claude.Frame) { frames <- f }, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	retained, completed, summary := false, false, false
	consume := func(f claude.Frame) {
		if f.Type == "control_request" {
			allowed := f.Request.ToolName == "Agent"
			if f.Request.ToolName == "Bash" {
				var in struct{ Command string }
				_ = json.Unmarshal(f.Request.Input, &in)
				allowed = in.Command == "sleep 2 && printf MIYA_BG_OK"
			}
			behavior := "deny"
			if allowed {
				behavior = "allow"
			}
			var input any
			_ = json.Unmarshal(f.Request.Input, &input)
			if err := c.Reply(f.RequestID, map[string]any{"behavior": behavior, "updatedInput": input, "message": "Only the synthetic lifecycle test is allowed"}); err != nil {
				t.Fatal(err)
			}
			if !allowed {
				t.Fatalf("unexpected live-test tool: %s", f.Request.ToolName)
			}
		}
		for _, task := range f.AgentTasks {
			if task.Status == "completed" {
				completed = true
			}
		}
		if f.Type == "result" && f.ParentToolUseID == "" {
			retained = retained || f.KeepingAlive && f.WaitingForAgents
			summary = summary || !f.KeepingAlive && strings.Contains(f.Result, "MIYA_BG_DONE")
		}
	}
	for {
		select {
		case f := <-frames:
			consume(f)
		case err := <-done:
			for len(frames) > 0 {
				consume(<-frames)
			}
			if err != nil || !retained || !completed || !summary {
				t.Fatalf("retained=%v completed=%v summary=%v error=%v", retained, completed, summary, err)
			}
			t.Log("real CLI retained process, delivered task completion and final summary")
			return
		case <-ctx.Done():
			t.Fatalf("live background task timed out: retained=%v completed=%v summary=%v", retained, completed, summary)
		}
	}
}
