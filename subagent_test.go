package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	claude "miya/internal/claude"
	codex "miya/internal/codex"
	controller "miya/internal/controller"
	subagent "miya/internal/subagent"
	uiruntime "miya/internal/uiruntime"
	os "os"
	strings "strings"
	testing "testing"
)

// claudeTestThread is a controller with one Claude conversation, and a
// function that streams a frame to it.
func claudeTestThread(t *testing.T) (*controller.Controller, func(string)) {
	c := testController()
	c.Active, c.SetActive = uiruntime.CreateSignal(1)
	c.Agent, c.SetAgent = uiruntime.CreateSignal("claude-code")
	c.SetThreads([]appmodel.Thread{{ID: 1, Agent: "claude-code", ServerID: "s1"}})
	return c, func(s string) {
		t.Helper()
		var f claude.Frame
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			t.Fatalf("frame does not decode: %v\n%s", err, s)
		}
		c.HandleClaude(1, f)
	}
}

func TestClaudeSubAgentFramesStayOnTheirTask(t *testing.T) {
	c, emit := claudeTestThread(t)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"task1","name":"Agent","input":{"description":"Find bugs","prompt":"Look for bugs"}}]}}`)
	emit(`{"type":"stream_event","parent_tool_use_id":"task1","event":{"type":"message_start","message":{"id":"sub1"}}}`)
	emit(`{"type":"stream_event","parent_tool_use_id":"task1","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Looking"}}}`)
	emit(`{"type":"assistant","parent_tool_use_id":"task1","message":{"id":"sub1","content":[{"type":"text","text":"Looking"},{"type":"tool_use","id":"read1","name":"Read","input":{"file_path":"/src/a.go"}}]}}`)
	emit(`{"type":"user","parent_tool_use_id":"task1","message":{"content":[{"type":"tool_result","tool_use_id":"read1","content":"code"}]}}`)
	messages := c.Current().Messages
	if len(messages) != 1 {
		t.Fatalf("sub-agent frames leaked into the conversation: %+v", messages)
	}
	a := messages[0].Agents
	if len(a) != 1 || a[0].Name != "Find bugs" || a[0].Status != subagent.Running || a[0].Steps != 1 || !strings.Contains(a[0].Detail, "a.go") {
		t.Fatalf("bad running sub-agent: %+v", a)
	}
	if got := subagent.Summary(a); !strings.Contains(got, "1 steps") || !strings.Contains(got, "a.go") {
		t.Fatalf("summary lost progress: %q", got)
	}
	emit(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"task1","content":[{"type":"text","text":"Found 2 bugs\nDetails"}]}]}}`)
	a = c.Current().Messages[0].Agents
	if a[0].Status != subagent.Completed || a[0].Detail != "Found 2 bugs" || subagent.AnyActive(a) {
		t.Fatalf("bad finished sub-agent: %+v", a)
	}
}

// The stream Claude Code 2.1.316 sent for a real sub-agent run, its
// environment frames (init, hooks) left out.
func TestClaudeSubAgentRecordedStream(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_subagent_stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	c, emit := claudeTestThread(t)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		emit(line)
	}
	var tools []appmodel.Message
	for _, m := range c.Current().Messages {
		if m.Role == "tool" {
			tools = append(tools, m)
		}
		if m.Role != "tool" && strings.Contains(m.Content, "Run `ls`") {
			t.Fatalf("sub-agent prompt leaked: %+v", m)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("sub-agent tool calls leaked: %+v", tools)
	}
	replies := 0
	for _, m := range c.Current().Messages {
		if m.Role == "assistant" {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("reply repeated: %d replies", replies)
	}
	if out := tools[0].Content; strings.Contains(out, "hand-back") || strings.Contains(out, `"type":"text"`) || !strings.Contains(out, "alpha.txt") {
		t.Fatalf("tool output not unwrapped: %q", out)
	}
	a := tools[0].Agents
	if len(a) != 1 || a[0].ID != "a82a491b71d0fe4c1" || a[0].Name != "List files" || a[0].Status != subagent.Completed || a[0].Steps != 1 || a[0].Detail != "The directory contains these files:" {
		t.Fatalf("bad recorded sub-agent: %+v", a)
	}
}

func TestClaudeBackgroundSubAgentFollowsItsTask(t *testing.T) {
	c, emit := claudeTestThread(t)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"bg1","name":"Agent","input":{"description":"Watch logs","prompt":"p","run_in_background":true}}]}}`)
	emit(`{"type":"system","subtype":"task_started","task_id":"t9","tool_use_id":"bg1","description":"Watch logs"}`)
	emit(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bg1","content":"Async agent launched"}]}}`)
	a := c.Current().Messages[0].Agents
	if a[0].Status != subagent.Running || a[0].ID != "t9" {
		t.Fatalf("background sub-agent ended with its call: %+v", a)
	}
	emit(`{"type":"system","subtype":"task_progress","task_id":"t9","tool_use_id":"bg1","description":"Reading logs","usage":{"tool_uses":2}}`)
	a = c.Current().Messages[0].Agents
	if a[0].Detail != "Reading logs" || a[0].Steps != 2 {
		t.Fatalf("progress lost: %+v", a)
	}
	emit(`{"type":"system","subtype":"task_updated","task_id":"t9","patch":{"status":"killed"}}`)
	a = c.Current().Messages[0].Agents
	if a[0].Status != subagent.Interrupted || subagent.AnyActive(a) {
		t.Fatalf("stop lost: %+v", a)
	}
}

func TestCodexCollabAgentStatesNormalize(t *testing.T) {
	m, ok := appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "inProgress", Tool: "wait", AgentsStates: json.RawMessage(`{"thread-b":{"status":"running","message":null},"thread-a":{"status":"errored","message":"boom"},"thread-c":{"status":"pendingInit"},"thread-d":{"status":"interrupted"}}`)})
	if !ok || len(m.Agents) != 4 {
		t.Fatalf("collab agents not normalized: %+v", m.Agents)
	}
	for i, want := range []string{subagent.Failed, subagent.Running, subagent.Pending, subagent.Interrupted} {
		if m.Agents[i].Status != want {
			t.Fatalf("agent %d: status %q, want %q", i, m.Agents[i].Status, want)
		}
	}
	if m.Agents[0].Detail != "boom" {
		t.Fatalf("agent message lost: %+v", m.Agents[0])
	}
	if got := subagent.Summary(m.Agents); got != "1 Running · 1 Waiting · 1 Failed · 1 Interrupted" {
		t.Fatalf("bad summary: %q", got)
	}
}

func TestCodexSpawnedAgentWithoutState(t *testing.T) {
	m, _ := appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "inProgress", Tool: "spawnAgent", ReceiverThreadIDs: []string{"019a7f3c-new"}, Prompt: "Review the parser\nin detail", AgentsStates: json.RawMessage(`{}`)})
	if len(m.Agents) != 1 || m.Agents[0].Name != "Review the parser" || m.Agents[0].Status != subagent.Running {
		t.Fatalf("spawned agent not shown: %+v", m.Agents)
	}
	m, _ = appmodel.ItemMessage(codex.Item{Type: "subAgentActivity", Kind: "started", AgentPath: "reviewer"})
	if m.Content != "Subagent started · reviewer" || len(m.Agents) != 0 {
		t.Fatalf("bad activity: %+v", m)
	}
}

func TestClaudeTaskReportUnwrapsHandBack(t *testing.T) {
	out := "  <tools-used-before-summary>Bash, Read</tools-used-before-summary>\n  \n[Subagent hand-back] Notes. The report follows:\n  Found 2 bugs\n  details\nagentId: a1 (use SendMessage)\n<usage>subagent_tokens: 10\ntool_uses: 3\nduration_ms: 5</usage>"
	if report, steps := appmodel.ClaudeTaskReport(out); report != "Found 2 bugs" || steps != 3 {
		t.Fatalf("bad report: %q, %d", report, steps)
	}
	if report, steps := appmodel.ClaudeTaskReport("plain answer\nmore"); report != "plain answer" || steps != 0 {
		t.Fatalf("bad plain report: %q, %d", report, steps)
	}
}

func claudeReplies(c *controller.Controller) string {
	var texts []string
	for _, m := range c.Current().Messages {
		if m.Role == "assistant" {
			texts = append(texts, m.Content)
		}
	}
	return strings.Join(texts, "|")
}

// Claude Code sends each reply block in its own assistant frame, at index 0
// there, while deltas carry the block's index in the whole reply.
func TestClaudeReplyBlocksDoNotRepeat(t *testing.T) {
	c, emit := claudeTestThread(t)
	emit(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`)
	emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"DO"}}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":"hm"}]}}`)
	emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"NE"}}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"DONE"}]}}`)
	emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"Second"}}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Second"}]}}`)
	if got := claudeReplies(c); got != "DONE|Second" {
		t.Fatalf("reply blocks repeated: %q", got)
	}
}

func TestClaudeUnstreamedReplyBlocksKeepEach(t *testing.T) {
	c, emit := claudeTestThread(t)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"First"}]}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Second"}]}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Second"}]}}`)
	if got := claudeReplies(c); got != "First|Second" {
		t.Fatalf("reply blocks lost or repeated: %q", got)
	}
}

// A sub-agent's progress changes only its message's Agents; the phone and
// the other windows must still get it.
func TestSubAgentProgressReachesPhone(t *testing.T) {
	th := appmodel.Thread{ID: 1, Agent: appmodel.AgentClaude, Messages: []appmodel.Message{{ID: 1, Role: "tool", Kind: "claudeTool", Content: "Agent\n{}", Status: "inProgress",
		Agents: []appmodel.SubAgent{{ID: "a", Name: "Watch logs", Status: subagent.Running}}}}}
	s := appmodel.NewPhoneThreadSync(nil)
	if _, ok := s.Update(th); !ok {
		t.Fatal("first update not sent")
	}
	th.Messages[0].Agents = []appmodel.SubAgent{{ID: "a", Name: "Watch logs", Status: subagent.Running, Steps: 2, Detail: "Reading logs"}}
	d, ok := s.Update(th)
	if !ok || len(d.Changed) != 1 {
		t.Fatalf("progress not sent: %+v", d)
	}
	th.Messages[0].Agents = []appmodel.SubAgent{{ID: "a", Name: "Watch logs", Status: subagent.Completed, Steps: 2, Detail: "Reading logs"}}
	d, ok = s.Update(th)
	if !ok || len(d.Changed) != 1 {
		t.Fatalf("background end not sent: %+v", d)
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got appmodel.PhoneThreadDelta
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if a := got.Changed[0].Agents; len(a) != 1 || a[0].Status != subagent.Completed || a[0].Steps != 2 || a[0].Detail != "Reading logs" {
		t.Fatalf("sub-agents lost on the wire: %+v", got.Changed[0])
	}
	if _, ok := s.Update(th); ok {
		t.Fatal("unchanged conversation sent again")
	}
}

func TestCodexCollabContentLeavesStatesToTheList(t *testing.T) {
	m, _ := appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "inProgress", Tool: "wait", AgentsStates: json.RawMessage(`{"thread-a":{"status":"errored","message":"boom"}}`)})
	if m.Content != "" || len(m.Agents) != 1 {
		t.Fatalf("states shown twice: %q", m.Content)
	}
	m, _ = appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "completed", Tool: "sendInput", ReceiverThreadIDs: []string{"thread-a"}, Prompt: " Also check the docs "})
	if m.Content != "Also check the docs" {
		t.Fatalf("prompt lost: %q", m.Content)
	}
	m, _ = appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "completed", Tool: "listAgents"})
	if len(m.Agents) != 0 || !strings.Contains(m.Content, "listAgents") {
		t.Fatalf("call without agents lost its summary: %+v", m)
	}
}

func TestCollapsedRunSummarizesLatestSubAgents(t *testing.T) {
	msgs := []appmodel.Message{
		{ID: 1, Role: "tool", Agents: []appmodel.SubAgent{{ID: "r", Status: subagent.Running}}},
		{ID: 2, Role: "tool", Kind: "commandExecution"},
		{ID: 3, Role: "tool", Agents: []appmodel.SubAgent{{ID: "r", Status: subagent.Completed}, {ID: "d", Status: subagent.Failed}}},
	}
	agents := subagent.Latest(msgs)
	if len(agents) != 2 || agents[0].Status != subagent.Completed || subagent.AnyActive(agents) {
		t.Fatalf("stale or repeated sub-agents: %+v", agents)
	}
	if got := subagent.Counts(agents); got != "1 Failed · 1 Completed" {
		t.Fatalf("bad counts: %q", got)
	}
}

func TestCodexSpawnPromptShownOnce(t *testing.T) {
	m, _ := appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "completed", Tool: "spawnAgent", ReceiverThreadIDs: []string{"a"}, Prompt: "Review the parser"})
	if m.Agents[0].Name != "Review the parser" || m.Content != "" {
		t.Fatalf("prompt shown twice: %+v", m)
	}
	m, _ = appmodel.ItemMessage(codex.Item{Type: "collabAgentToolCall", Status: "completed", Tool: "spawnAgent", ReceiverThreadIDs: []string{"a"}, Prompt: "Review the parser\nFocus on edge cases"})
	if m.Agents[0].Name != "Review the parser" || !strings.Contains(m.Content, "Focus on edge cases") {
		t.Fatalf("long prompt lost: %+v", m)
	}
}
