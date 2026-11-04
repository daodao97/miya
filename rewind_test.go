//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// drainPending runs the work dispatched while no window or inline
// dispatch exists, on the test's goroutine.
func drainPending() {
	windowMu.Lock()
	fns := pending
	pending = nil
	windowMu.Unlock()
	for _, f := range fns {
		f()
	}
}

func pendingCount() int {
	windowMu.Lock()
	defer windowMu.Unlock()
	return len(pending)
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

	messages, err := parseClaudeSession(path)
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

	id, err := forkClaudeSessionBefore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	forked := filepath.Join(filepath.Dir(path), id+".jsonl")
	data, _ := os.ReadFile(forked)
	text := string(data)
	if strings.Contains(text, "second") || strings.Contains(text, `"s1"`) || strings.Contains(text, `"u1"`) || strings.Contains(text, "progress") {
		t.Fatalf("fork kept the later turn or old IDs:\n%s", text)
	}
	kept, err := parseClaudeSession(forked)
	if err != nil || len(kept) != 2 || kept[0].Content != "first" || kept[1].Content != "reply one" {
		t.Fatalf("forked history %+v %v", kept, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(original) {
		t.Fatal("original session changed")
	}
	if _, err := forkClaudeSessionBefore(path, 0); err == nil {
		t.Fatal("forked before the first prompt")
	}
	if _, err := forkClaudeSessionBefore(path, 5); err == nil {
		t.Fatal("forked before a prompt that does not exist")
	}
}

func TestSupersededSessionsAreNotListed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := "/work/demo"
	writeClaudeSession(t, cwd, "s1", sessionLines(cwd)...)
	if len(listClaudeSessions(cwd)) != 1 {
		t.Fatal("session not listed")
	}
	markClaudeSuperseded("s1")
	if len(listClaudeSessions(cwd)) != 0 {
		t.Fatal("superseded session listed")
	}
}

func TestClaudeRewindForksAndResends(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	syncPanel(t)
	inlineDispatch.Store(false)
	t.Cleanup(func() { windowMu.Lock(); pending = nil; windowMu.Unlock() })

	cwd := t.TempDir()
	path := writeClaudeSession(t, cwd, "s1", sessionLines(cwd)...)
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MIYA_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := testController()
	c.cwd, c.ctx = cwd, context.Background()
	c.active, c.setActive = createSignal(0)
	c.agent, c.setAgent = createSignal(agentCodex)
	c.draft, c.setDraft = createSignal("")
	c.attachments, c.setAttachments = createSignal([]string{})
	c.claudeBinary, c.claudeEnv = bin, []string{"MIYA_OUT=" + out}
	messages, err := parseClaudeSession(path)
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
	c.setThreads([]Thread{{ID: 9, Agent: agentClaude, ServerID: "s1", Resumed: true, Loaded: true, HistoryLoaded: true, Messages: messages}})
	c.setActive(9)

	if !c.canRewind(c.current(), messages[2]) {
		t.Fatal("Claude message not editable")
	}
	c.rewind(9, second, "second, edited")
	drainPending()

	cur := c.current()
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
	for _, s := range listClaudeSessions(cwd) {
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
	c.ready, c.setReady = createSignal(false)
	b := codexBackend{}
	th := Thread{ServerID: "t1"}
	m := Message{Role: "user", TurnID: "turn1"}
	if b.CanRewind(c, th, m) {
		t.Fatal("rewind without a connection")
	}
	c.setReady(true)
	if !b.CanRewind(c, th, m) {
		t.Fatal("no rewind once connected")
	}
	if b.CanRewind(c, Thread{}, m) || b.CanRewind(c, th, Message{Role: "user"}) {
		t.Fatal("rewind without a thread or a turn")
	}
}

func TestEditMessageInPlace(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.claudeBinary = "/bin/true"
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Title: "Edit me", HistoryLoaded: true, Resumed: true,
		Messages: []Message{{ID: 10, Role: "user", Content: "original prompt"}, {ID: 11, Role: "assistant", Content: "answer", Status: "completed"}}}})
	c.setActive(1)
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
	if app.editing != 10 || app.editText != "original prompt" || !tt.HasText("Restart from this message. Later messages will be deleted. Miya will not undo file changes already made.") {
		t.Fatalf("editor not open: %d %q", app.editing, app.editText)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.editing != 0 {
		t.Fatal("Escape did not close the editor")
	}
	app.startEdit(c.current().Messages[0])
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil || app.editing != 0 {
		t.Fatalf("Cancel did not close the editor: %v", err)
	}
	c.update(1, func(t *Thread) { t.Busy = true })
	tt.Move(r.X+4, r.Y+4)
	tt.Frame()
	if _, ok := tt.Find("Edit message"); ok {
		t.Fatal("edit offered while the conversation runs")
	}
}
