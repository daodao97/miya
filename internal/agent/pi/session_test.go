package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/model"
)

const sessionFixture = `{"type":"session","version":3,"id":"s1","cwd":"/p"}
{"type":"model_change","id":"a","parentId":null}
{"type":"message","id":"u1","parentId":"a","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1000}}
{"type":"message","id":"x1","parentId":"u1","message":{"role":"assistant","content":[{"type":"text","text":"abandoned branch"}],"timestamp":1500}}
{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"looking"},{"type":"toolCall","id":"t1","name":"read","arguments":{"path":"a.go"}}],"stopReason":"toolUse","timestamp":2000}}
{"type":"message","id":"r1","parentId":"a1","message":{"role":"toolResult","toolCallId":"t1","toolName":"read","content":[{"type":"text","text":"package a"}],"isError":false,"timestamp":2500}}
{"type":"message","id":"a2","parentId":"r1","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop","timestamp":3000}}
`

func writeSession(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(sessionFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSessionMessagesReadsActiveBranch(t *testing.T) {
	messages, err := SessionMessages(writeSession(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range messages {
		got = append(got, m.Role+":"+strings.SplitN(m.Content, "\n", 2)[0])
	}
	want := []string{"user:hello", "activity:plan", "assistant:looking", "tool:Read", "assistant:done"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages = %v, want %v", got, want)
	}
	if tool := messages[3]; !strings.Contains(tool.Content, "package a") || tool.Status != "completed" || tool.CompletedAtMs != 2500 {
		t.Fatalf("tool = %+v", tool)
	}
}

// A record whose messages were lost still shows the conversation pi kept.
func TestHistoryFallsBackToSessionFile(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	pinative.SaveRecord(model.Thread{ServerID: "s1", Agent: "pi"}, writeSession(t))
	messages, err := History("s1")
	if err != nil || len(messages) != 5 {
		t.Fatalf("History = %d messages, %v", len(messages), err)
	}
}

func TestHistoryReportsUnavailableSessionFallback(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.jsonl"), t.TempDir()} {
		pinative.SaveRecord(model.Thread{ServerID: "s1", Agent: "pi"}, path)
		if _, err := History("s1"); err == nil {
			t.Fatalf("unreadable session %q was reported as successful empty history", path)
		}
	}
}

// Suspending a conversation whose history is not loaded keeps the saved
// messages.
func TestSaveRecordKeepsMessagesOfUnloadedConversation(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	pinative.SaveRecord(model.Thread{ServerID: "s1", HistoryLoaded: true, Messages: []model.Message{{Role: "user", Content: "hi"}}}, "/session.jsonl")
	pinative.SaveRecord(model.Thread{ServerID: "s1", Title: "renamed"}, "")
	rec, err := pinative.LoadRecord("s1")
	if err != nil || len(rec.Thread.Messages) != 1 || rec.SessionFile != "/session.jsonl" || rec.Thread.Title != "renamed" {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	pinative.SaveRecord(model.Thread{ServerID: "s1", HistoryLoaded: true}, "")
	if rec, _ := pinative.LoadRecord("s1"); len(rec.Thread.Messages) != 0 {
		t.Fatal("a loaded conversation's empty history was not saved")
	}
}

// Checks a real session file when PI_SESSION_FILE names one.
func TestSessionMessagesRealFile(t *testing.T) {
	path := os.Getenv("PI_SESSION_FILE")
	if path == "" {
		t.Skip("PI_SESSION_FILE not set")
	}
	messages, err := SessionMessages(path)
	if err != nil || len(messages) == 0 {
		t.Fatalf("%d messages, %v", len(messages), err)
	}
	counts := map[string]int{}
	for _, m := range messages {
		counts[m.Role]++
	}
	t.Logf("%d messages: %v; last: %.80q", len(messages), counts, messages[len(messages)-1].Content)
}
