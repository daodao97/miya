//go:build !windows

package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"miya/internal/agent/claude/native/transcript"
	codex "miya/internal/agent/codex/native"
	"miya/internal/memory"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

func TestMemoryInjectedIntoCodexWithoutChangingUserTextOrHistory(t *testing.T) {
	c, d, log := codexResumeFixture(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.Cwd)
	if err := c.Memories().Store.Configure(scope, memory.Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	saved, err := c.Memories().Store.Save(scope, memory.Mutation{Kind: "fact", Content: "手机构建必须使用 scripts/build-ios.sh", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	query := "手机构建入口是什么"
	if c.Submit(model.SendRequest{ThreadID: 1, Text: query}) != 1 {
		t.Fatal(c.LastSubmitError)
	}
	waitCodexResume(t, c, d, func() bool { return !c.ThreadWithID(1).Busy && len(c.ThreadWithID(1).Messages) > 2 })
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
	if texts := userTexts(c.ThreadWithID(1)); len(texts) == 0 || texts[len(texts)-1] != query || strings.Contains(strings.Join(texts, "\n"), "miya-memory") {
		t.Fatalf("user bubble changed: %q", texts)
	}
	content, _ := json.Marshal([]map[string]any{{"type": "text", "text": augmented}})
	m, ok := msgfmt.ItemMessage(codex.Item{Type: "userMessage", Content: content})
	if !ok || m.Content != query {
		t.Fatalf("history leaked injected content: %q", m.Content)
	}
	if got := transcript.VisibleText([]transcript.Block{{Type: "text", Text: augmented}}); got != query {
		t.Fatalf("Claude history leaked: %q", got)
	}
	req := model.SendRequest{ThreadID: 1, Text: query, KeepSessionSettings: true}
	if c.Memories().PrepareInput(c.ThreadWithID(1), req, c.Cwd, c.ThreadRef(c.ThreadWithID(1)).URL()).Text(query) != query {
		t.Fatal("native task control injected memory")
	}
}

func TestMemoryQueueRecallsAtExecutionAndFailuresDoNotBlockSend(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.Cwd)
	_ = c.Memories().Store.Configure(scope, memory.Settings{AutoInject: true})
	now := time.Now()
	old, err := c.Memories().Store.Save(scope, memory.Mutation{Kind: "fact", Content: "手机构建旧约定", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Submit(model.SendRequest{ThreadID: 1, Text: "手机构建"}) != 1 {
		t.Fatal("queue rejected")
	}
	if len(c.ThreadWithID(1).Queue) != 1 {
		t.Fatal("missing queue")
	}
	_, err = c.Memories().Store.Save(scope, memory.Mutation{ID: old.ID, Version: old.Version, Kind: "fact", Content: "手机构建新约定", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	req := c.ThreadWithID(1).Queue[0].SendRequest
	input := c.Memories().PrepareInput(c.ThreadWithID(1), req, c.Cwd, "session")
	if text := input.Text(req.Text); !strings.Contains(text, "手机构建新约定") || strings.Contains(text, "手机构建旧约定") {
		t.Fatalf("queued recall frozen too early: %q", text)
	}
	_ = c.Memories().Store.Configure(scope, memory.Settings{Disabled: true})
	if got := input.Text(req.Text); got != req.Text {
		t.Fatal("disabled retry retained memory")
	}
	c.Update(1, func(t *model.Thread) { t.Busy = false })
	d.drain()
	if got := userTexts(c.ThreadWithID(1)); len(got) != 1 || got[0] != "手机构建" {
		t.Fatal("memory failure blocked normal send")
	}
}

func TestMemoryAbsolutePathMessagesAreNotSlashCommands(t *testing.T) {
	c, _ := queueController(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.Cwd)
	if err := c.Memories().Store.Configure(scope, memory.Settings{AutoInject: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, err := c.Memories().Store.Save(scope, memory.Mutation{Kind: "fact", Content: "项目构建使用 build-ios.sh", ObservedAt: now, Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	th := c.ThreadWithID(1)
	text := "/Users/daodao/project 项目构建入口是什么"
	if c.Memories().BeginTurn(th, model.SendRequest{Text: text}, 10) == "" {
		t.Fatal("absolute path incorrectly disabled extraction")
	}
	input := c.Memories().PrepareInput(th, model.SendRequest{Text: text}, c.Cwd, c.ThreadRef(th).URL())
	if !strings.Contains(input.Text(text), "build-ios.sh") {
		t.Fatal("absolute path incorrectly disabled recall")
	}
	for _, command := range []string{"/compact", "/review changes", "/goal run", "/loop 5m check", "/custom-skill"} {
		if c.Memories().BeginTurn(th, model.SendRequest{Text: command}, 20) != "" {
			t.Fatalf("command unexpectedly enabled extraction: %s", command)
		}
	}
}
