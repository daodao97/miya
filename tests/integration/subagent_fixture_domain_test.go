package integration

import (
	"miya/internal/chat/model"
	"miya/internal/chat/subagent"
	"os"
	"strings"
	"testing"
)

// The stream Claude Code 2.1.316 sent for a real sub-agent run, its
// environment frames (init, hooks) left out.
func TestNSGoClaudeSubAgentRecordedStream(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_subagent_stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	c, emit := claudeTestThread(t)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		emit(line)
	}
	var tools []model.Message
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
