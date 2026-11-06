package automation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	claude "miya/internal/agent/claude/native"
	"miya/internal/chat/model"
)

func TestAutomationDraftPromptAndAnswers(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "main.go", ".env"} {
		_ = os.WriteFile(filepath.Join(dir, name), nil, 0o600)
	}
	_ = os.Mkdir(filepath.Join(dir, "internal"), 0o700)
	p := automationDraftPrompt("每天 9 点跑测试", dir, atLocal("2026-11-01 10:00"))
	for _, want := range []string{"go.mod", "internal/", "2026-11-01 10:00 Sun", "每天 9 点跑测试", defaultAutomationSchedule} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, ".env") {
		t.Fatal("hidden files listed")
	}
	out := []byte(`{"type":"result","is_error":false,"result":"{}","structured_output":{"name":"每日分拣","schedule":"0 9 * * 1-5","check":"go test ./...","rounds":3,"instructions":"看 Issue","time_missing":false}}`)
	raw, err := claude.StructuredOutput(out, "draft the automation")
	if err != nil || !strings.Contains(string(raw), "每日分拣") {
		t.Fatalf("structured output not read: %s %v", raw, err)
	}
	if _, err := claude.StructuredOutput([]byte(`{"is_error":true,"result":"Not logged in"}`), "draft the automation"); err == nil || !strings.Contains(err.Error(), "Not logged in") || !strings.Contains(err.Error(), "could not draft the automation") {
		t.Fatalf("error not reported: %v", err)
	}
}

func TestAutomationFromDraft(t *testing.T) {
	a, missing, err := FromDraft(Draft{Name: "每日分拣", Schedule: "0 9 * * 1-5", Check: "go test ./...", Rounds: 3, Instructions: "看 Issue"}, model.AgentClaude, "/ws", " 每个工作日 9 点 ")
	if err != nil || missing || a.Rounds != 3 || a.Request != "每个工作日 9 点" || a.Agent != model.AgentClaude {
		t.Fatalf("bad draft: %+v %v %v", a, missing, err)
	}
	a, missing, err = FromDraft(Draft{Name: "", Schedule: "", Instructions: "", TimeMissing: true}, model.AgentCodex, "/ws", "看一下新 Issue 能修就修")
	if err != nil || !missing || a.Schedule != defaultAutomationSchedule || a.Instructions != "看一下新 Issue 能修就修" || a.Name == "" || a.Rounds != 1 {
		t.Fatalf("defaults not filled: %+v %v %v", a, missing, err)
	}
	if a, _, _ := FromDraft(Draft{Name: "x", Schedule: "0 9 * * *", Instructions: "y", Check: "cd /ws && go test ./..."}, model.AgentCodex, "/ws", "y"); a.Check != "go test ./..." {
		t.Fatalf("cd into the project kept: %q", a.Check)
	}
	if _, _, err := FromDraft(Draft{Name: "x", Schedule: "every day", Instructions: "y"}, model.AgentCodex, "/ws", "y"); err == nil {
		t.Fatal("bad schedule accepted")
	}
}
