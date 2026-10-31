package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
