//go:build !windows

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent/claude/native/transcript"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/uiruntime"
)

// drainPending runs the work dispatched while no window or inline
// dispatch exists, on the test's goroutine.
func drainPending() {
	uiruntime.WindowMu.Lock()
	fns := uiruntime.Pending
	uiruntime.Pending = nil
	uiruntime.WindowMu.Unlock()
	for _, f := range fns {
		f()
	}
}

func pendingCount() int {
	uiruntime.WindowMu.Lock()
	defer uiruntime.WindowMu.Unlock()
	return len(uiruntime.Pending)
}

var twoTurns = []string{
	`{"type":"user","uuid":"u1","sessionId":"s1","session_id":"s1","cwd":"CWD","timestamp":"2026-10-31T01:00:00Z","message":{"role":"user","content":"first"}}`,
	`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"s1","timestamp":"2026-10-31T01:00:01Z","message":{"content":[{"type":"text","text":"reply one"}]}}`,
	`{"type":"progress","uuid":"p1","parentUuid":"a1","sessionId":"s1"}`,
	`{"type":"user","uuid":"x1","parentUuid":"p1","sessionId":"s1","message":{"content":"[Request interrupted by user]"}}`,
	`{"type":"user","uuid":"u2","parentUuid":"x1","sessionId":"s1","timestamp":"2026-10-31T01:01:00Z","message":{"role":"user","content":"second"}}`,
	`{"type":"assistant","uuid":"a2","parentUuid":"u2","sessionId":"s1","timestamp":"2026-10-31T01:01:01Z","message":{"content":[{"type":"text","text":"reply two"}]}}`,
}

func sessionLines(cwd string) []string {
	out := make([]string, len(twoTurns))
	for i, l := range twoTurns {
		out[i] = strings.ReplaceAll(l, "CWD", cwd)
	}
	return out
}

func TestForkClaudeSessionBefore(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := "/work/demo"
	path := writeClaudeSession(t, cwd, "s1", sessionLines(cwd)...)
	original, _ := os.ReadFile(path)

	messages, err := transcript.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if strings.Contains(m.Content, "interrupted") {
			t.Fatalf("interrupt marker shown: %+v", messages)
		}
	}
	if len(messages) != 4 {
		t.Fatalf("messages %+v", messages)
	}

	id, err := transcript.ForkBefore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	forked := filepath.Join(filepath.Dir(path), id+".jsonl")
	data, _ := os.ReadFile(forked)
	text := string(data)
	if strings.Contains(text, "second") || strings.Contains(text, `"s1"`) || strings.Contains(text, `"u1"`) || strings.Contains(text, "progress") {
		t.Fatalf("fork kept the later turn or old IDs:\n%s", text)
	}
	kept, err := transcript.Parse(forked)
	if err != nil || len(kept) != 2 || kept[0].Content != "first" || kept[1].Content != "reply one" {
		t.Fatalf("forked history %+v %v", kept, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(original) {
		t.Fatal("original session changed")
	}
	if _, err := transcript.ForkBefore(path, 0); err == nil {
		t.Fatal("forked before the first prompt")
	}
	if _, err := transcript.ForkBefore(path, 5); err == nil {
		t.Fatal("forked before a prompt that does not exist")
	}
}

func TestSupersededSessionsAreNotListed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := "/work/demo"
	writeClaudeSession(t, cwd, "s1", sessionLines(cwd)...)
	if len(transcript.List(cwd)) != 1 {
		t.Fatal("session not listed")
	}
	transcript.MarkSuperseded("s1")
	if len(transcript.List(cwd)) != 0 {
		t.Fatal("superseded session listed")
	}
}

func TestClaudeRewindForksAndResends(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	syncPanel(t)
	uiruntime.InlineDispatch.Store(false)
	t.Cleanup(func() { uiruntime.WindowMu.Lock(); uiruntime.Pending = nil; uiruntime.WindowMu.Unlock() })

	cwd := t.TempDir()
	path := writeClaudeSession(t, cwd, "s1", sessionLines(cwd)...)
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MIYA_OUT.tmp\"\nmv \"$MIYA_OUT.tmp\" \"$MIYA_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := testController()
	c.Cwd, c.Ctx = cwd, context.Background()
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Agent, c.SetAgent = uiruntime.CreateSignal(appmodel.AgentCodex)
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.ClaudeBinary, c.ClaudeEnv = bin, []string{"MIYA_OUT=" + out}
	messages, err := transcript.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	second := 0
	for i := range messages {
		messages[i].ID = 100 + i
		if messages[i].Content == "second" {
			second = messages[i].ID
		}
	}
	c.SetThreads([]model.Thread{{ID: 9, Agent: appmodel.AgentClaude, ServerID: "s1", Resumed: true, Loaded: true, HistoryLoaded: true, Messages: messages}})
	c.SetActive(9)

	if !c.CanRewind(c.Current(), messages[2]) {
		t.Fatal("Claude message not editable")
	}
	c.Rewind(9, second, "second, edited")
	drainPending()

	cur := c.Current()
	if cur.ServerID == "s1" || !cur.Resumed {
		t.Fatalf("session not forked: %+v", cur)
	}
	var texts []string
	for _, m := range cur.Messages {
		texts = append(texts, m.Role+":"+m.Content)
	}
	if got := strings.Join(texts, " | "); got != "user:first | assistant:reply one | user:second, edited" {
		t.Fatalf("messages after rewind: %s", got)
	}
	if !cur.Busy {
		t.Fatal("edited message was not sent")
	}
	var args []string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(out); err == nil && len(data) > 0 {
			args = strings.Split(strings.TrimSpace(string(data)), "\n")
			break
		}
	}
	resumed := ""
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) {
			resumed = args[i+1]
		}
	}
	if resumed != cur.ServerID {
		t.Fatalf("resumed %q, want the fork %q (args %q)", resumed, cur.ServerID, args)
	}
	for _, s := range transcript.List(cwd) {
		if s.ID == "s1" {
			t.Fatal("the replaced session is still listed")
		}
	}
	// Let the run report its end before the test's state goes away.
	for deadline := time.Now().Add(time.Minute); pendingCount() < 2 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}
}

func TestCodexCanRewind(t *testing.T) {
	c := testController()
	c.Ready, c.SetReady = uiruntime.CreateSignal(false)
	b := c.BackendFor(model.AgentCodex)
	th := model.Thread{ServerID: "t1"}
	m := model.Message{Role: "user", TurnID: "turn1"}
	if b.CanRewind(th, m) {
		t.Fatal("rewind without a connection")
	}
	c.SetReady(true)
	if !b.CanRewind(th, m) {
		t.Fatal("no rewind once connected")
	}
	if b.CanRewind(model.Thread{}, m) || b.CanRewind(th, model.Message{Role: "user"}) {
		t.Fatal("rewind without a thread or a turn")
	}
}

func TestEditMessageInPlace(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.ClaudeBinary = "/bin/true"
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Title: "Edit me", HistoryLoaded: true, Resumed: true,
		Messages: []model.Message{{ID: 10, Role: "user", Content: "original prompt"}, {ID: 11, Role: "assistant", Content: "answer", Status: "completed"}}}})
	c.SetActive(1)
	tt.Frame()
	r, ok := tt.Find("original prompt")
	if !ok {
		t.Fatalf("message missing: %q", tt.Texts())
	}
	tt.Move(r.X+4, r.Y+4)
	tt.Frame()
	if err := tt.Click("Edit message"); err != nil {
		t.Fatalf("no edit button: %v", err)
	}
	if app.Editing != 10 || app.EditText != "original prompt" || !tt.HasText("Restart from this message. Later messages will be deleted. Miya will not undo file changes already made.") {
		t.Fatalf("editor not open: %d %q", app.Editing, app.EditText)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Editing != 0 {
		t.Fatal("Escape did not close the editor")
	}
	app.StartEdit(c.Current().Messages[0])
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil || app.Editing != 0 {
		t.Fatalf("Cancel did not close the editor: %v", err)
	}
	c.Update(1, func(t *model.Thread) { t.Busy = true })
	tt.Move(r.X+4, r.Y+4)
	tt.Frame()
	if _, ok := tt.Find("Edit message"); ok {
		t.Fatal("edit offered while the conversation runs")
	}
}
