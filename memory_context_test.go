//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"miya/internal/codex"
	"miya/internal/memory"
)

func TestMemoryInjectedIntoCodexWithoutChangingUserTextOrHistory(t *testing.T) {
	c, d, log := codexResumeFixture(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.cwd)
	if err := c.memory.Configure(scope, memory.Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	saved, err := c.memory.Save(scope, memory.Mutation{Kind: "fact", Content: "手机构建必须使用 scripts/build-ios.sh", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	query := "手机构建入口是什么"
	if c.submit(sendRequest{ThreadID: 1, Text: query}) != 1 {
		t.Fatal(c.lastSubmitError)
	}
	waitCodexResume(t, c, d, func() bool { return !c.threadWithID(1).Busy && len(c.threadWithID(1).Messages) > 2 })
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var augmented string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var call struct {
			Method string
			Params struct{ Input []struct{ Text string } }
		}
		_ = json.Unmarshal([]byte(line), &call)
		if call.Method == "turn/start" && len(call.Params.Input) > 0 {
			augmented = call.Params.Input[0].Text
		}
	}
	if !strings.HasPrefix(augmented, query) || !strings.Contains(augmented, saved.Content) {
		t.Fatalf("backend input omitted recall: %q", augmented)
	}
	if texts := userTexts(c.threadWithID(1)); len(texts) == 0 || texts[len(texts)-1] != query || strings.Contains(strings.Join(texts, "\n"), "miya-memory") {
		t.Fatalf("user bubble changed: %q", texts)
	}
	content, _ := json.Marshal([]map[string]any{{"type": "text", "text": augmented}})
	m, ok := itemMessage(codex.Item{Type: "userMessage", Content: content})
	if !ok || m.Content != query {
		t.Fatalf("history leaked injected content: %q", m.Content)
	}
	if got := claudeVisibleText([]claudeBlock{{Type: "text", Text: augmented}}); got != query {
		t.Fatalf("Claude history leaked: %q", got)
	}
	req := sendRequest{ThreadID: 1, Text: query, KeepSessionSettings: true}
	if c.prepareMemoryInput(c.threadWithID(1), req, c.cwd, c.threadRef(c.threadWithID(1)).URL()).text(query) != query {
		t.Fatal("native task control injected memory")
	}
}

func TestMemoryQueueRecallsAtExecutionAndFailuresDoNotBlockSend(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.cwd)
	_ = c.memory.Configure(scope, memory.Settings{AutoInject: true})
	now := time.Now()
	old, err := c.memory.Save(scope, memory.Mutation{Kind: "fact", Content: "手机构建旧约定", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.submit(sendRequest{ThreadID: 1, Text: "手机构建"}) != 1 {
		t.Fatal("queue rejected")
	}
	if len(c.threadWithID(1).Queue) != 1 {
		t.Fatal("missing queue")
	}
	_, err = c.memory.Save(scope, memory.Mutation{ID: old.ID, Version: old.Version, Kind: "fact", Content: "手机构建新约定", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	req := c.threadWithID(1).Queue[0].sendRequest
	input := c.prepareMemoryInput(c.threadWithID(1), req, c.cwd, "session")
	if text := input.text(req.Text); !strings.Contains(text, "手机构建新约定") || strings.Contains(text, "手机构建旧约定") {
		t.Fatalf("queued recall frozen too early: %q", text)
	}
	_ = c.memory.Configure(scope, memory.Settings{Disabled: true})
	if got := input.text(req.Text); got != req.Text {
		t.Fatal("disabled retry retained memory")
	}
	c.update(1, func(t *Thread) { t.Busy = false })
	d.drain()
	if got := userTexts(c.threadWithID(1)); len(got) != 1 || got[0] != "手机构建" {
		t.Fatal("memory failure blocked normal send")
	}
}

func TestMemoryAbsolutePathMessagesAreNotSlashCommands(t *testing.T) {
	c, _ := queueController(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.cwd)
	if err := c.memory.Configure(scope, memory.Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, err := c.memory.Save(scope, memory.Mutation{Kind: "fact", Content: "项目构建使用 build-ios.sh", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	th := c.threadWithID(1)
	text := "/Users/daodao/project 项目构建入口是什么"
	if c.beginMemoryTurn(th, sendRequest{Text: text}, 10) == "" {
		t.Fatal("absolute path incorrectly disabled extraction")
	}
	input := c.prepareMemoryInput(th, sendRequest{Text: text}, c.cwd, c.threadRef(th).URL())
	if !strings.Contains(input.text(text), "build-ios.sh") {
		t.Fatal("absolute path incorrectly disabled recall")
	}
	for _, command := range []string{"/compact", "/review changes", "/goal run", "/loop 5m check", "/custom-skill"} {
		if c.beginMemoryTurn(th, sendRequest{Text: command}, 20) != "" {
			t.Fatalf("command unexpectedly enabled extraction: %s", command)
		}
	}
}
