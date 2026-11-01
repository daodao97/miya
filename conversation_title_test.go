package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeCommandTitles(t *testing.T) {
	for _, tc := range []struct{ prompt, want string }{
		{"修复  登录\n问题", "修复 登录 问题"},
		{"/unslop README.md", "/unslop README.md"},
		{"<command-message>unslop</command-message> <command-name>/unslop</command-name>", "/unslop"},
		{"<command-message>unslop</command-message>\n<command-name>/unslop</command-name>\n<command-args>清理 README.md 的文案</command-args>", "/unslop 清理 README.md 的文案"},
		{"<command-message>review</command-message>", "/review"},
	} {
		if got := claudePromptTitle(tc.prompt); got != tc.want {
			t.Errorf("title of %q = %q; want %q", tc.prompt, got, tc.want)
		}
	}
}

func TestClaudeCachedCommandTitleRefreshesWithoutNewActivity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	inlineDispatch.Store(true)
	t.Cleanup(func() { inlineDispatch.Store(false) })
	c := testController()
	c.cwd = t.TempDir()
	c.active, c.setActive = createSignal(0)
	prompt := "<command-message>unslop</command-message> <command-name>/unslop</command-name> <command-args>README.md</command-args>"
	entry, err := json.Marshal(map[string]any{"type": "user", "cwd": c.cwd, "message": map[string]string{"content": prompt}})
	if err != nil {
		t.Fatal(err)
	}
	path := writeClaudeSession(t, c.cwd, "command", string(entry))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	at := info.ModTime().UnixMilli()
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "command", Title: clippedTitle(prompt), LastActivityAt: at}})
	c.loadClaudeSessionsSync()
	th := c.threadWithID(1)
	if th.Title != "/unslop README.md" || th.LastActivityAt != at {
		t.Fatalf("cached title was not repaired without changing activity: %+v", th)
	}
}

func TestConversationTitlePriority(t *testing.T) {
	for _, tc := range []struct{ name, preview, existing, want string }{
		{"Named task", "first request", "old", "Named task"},
		{"", "first\nrequest", "old", "first request"},
		{"", "", "old", "old"},
		{" ", "\n", "", "Untitled thread"},
	} {
		if got := conversationTitle(tc.name, tc.preview, tc.existing); got != tc.want {
			t.Fatalf("%+v: %q", tc, got)
		}
	}
	title := conversationTitle("", strings.Repeat("中", 60), "")
	if len([]rune(title)) != 46 || !strings.HasSuffix(title, "…") {
		t.Fatalf("invalid unicode clipping: %q", title)
	}
}
func TestClaudeRecentTitlePriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	prefix := `{"type":"user","cwd":"/work","sessionId":"session","message":{"content":"initial request"}}` + "\n" + `{"type":"custom-title","customTitle":"My name"}` + "\n"
	filler := strings.Repeat("{}\n", claudeSummaryReadLimit/3+10)
	if err := os.WriteFile(path, []byte(prefix+filler+`{"type":"ai-title","aiTitle":"Recent generated name"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, ok := summarizeClaudeSession(path)
	if !ok || s.Title != "My name" {
		t.Fatalf("custom name lost: %+v", s)
	}
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString("{\"type\":\"custom-title\",\"customTitle\":\"Latest name\"}\n")
	f.Close()
	s, ok = summarizeClaudeSession(path)
	if !ok || s.Title != "Latest name" {
		t.Fatalf("tail name missed: %+v", s)
	}
}
