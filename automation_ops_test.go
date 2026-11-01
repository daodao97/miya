package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomationDraftPromptAndAnswers(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "main.go", ".env"} {
		_ = os.WriteFile(filepath.Join(dir, name), nil, 0o600)
	}
	_ = os.Mkdir(filepath.Join(dir, "internal"), 0o700)
	p := automationDraftPrompt("每天 9 点跑测试", dir, atLocal("2026-11-01 10:00"))
	for _, want := range []string{"go.mod", "internal/", "2026-11-01 10:00 周日", "每天 9 点跑测试", defaultAutomationSchedule} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, ".env") {
		t.Fatal("hidden files listed")
	}
	out := []byte(`{"type":"result","is_error":false,"result":"{}","structured_output":{"name":"每日分拣","schedule":"0 9 * * 1-5","check":"go test ./...","rounds":3,"instructions":"看 Issue","time_missing":false}}`)
	raw, err := claudeStructuredOutput(out)
	if err != nil || !strings.Contains(string(raw), "每日分拣") {
		t.Fatalf("structured output not read: %s %v", raw, err)
	}
	if _, err := claudeStructuredOutput([]byte(`{"is_error":true,"result":"Not logged in"}`)); err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Fatalf("error not reported: %v", err)
	}
}

func TestAutomationFromDraft(t *testing.T) {
	a, missing, err := automationFromDraft(automationDraft{Name: "每日分拣", Schedule: "0 9 * * 1-5", Check: "go test ./...", Rounds: 3, Instructions: "看 Issue"}, agentClaude, "/ws", " 每个工作日 9 点 ")
	if err != nil || missing || a.Rounds != 3 || a.Request != "每个工作日 9 点" || a.Agent != agentClaude {
		t.Fatalf("bad draft: %+v %v %v", a, missing, err)
	}
	a, missing, err = automationFromDraft(automationDraft{Name: "", Schedule: "", Instructions: "", TimeMissing: true}, agentCodex, "/ws", "看一下新 Issue 能修就修")
	if err != nil || !missing || a.Schedule != defaultAutomationSchedule || a.Instructions != "看一下新 Issue 能修就修" || a.Name == "" || a.Rounds != 1 {
		t.Fatalf("defaults not filled: %+v %v %v", a, missing, err)
	}
	if a, _, _ := automationFromDraft(automationDraft{Name: "x", Schedule: "0 9 * * *", Instructions: "y", Check: "cd /ws && go test ./..."}, agentCodex, "/ws", "y"); a.Check != "go test ./..." {
		t.Fatalf("cd into the project kept: %q", a.Check)
	}
	if _, _, err := automationFromDraft(automationDraft{Name: "x", Schedule: "every day", Instructions: "y"}, agentCodex, "/ws", "y"); err == nil {
		t.Fatal("bad schedule accepted")
	}
}

func TestAutomationOps(t *testing.T) {
	h := newAutomationHarness(t)
	c := h.c
	var saved automationView
	draft := Automation{Name: "日报", Project: c.cwd, Agent: agentCodex, Instructions: "写日报", Schedule: "0 18 * * *", Enabled: true}
	if err := c.automationCall("automationSave", automationRequest{Automation: &draft}, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.ScheduleText != "每天 18:00" || !saved.Next.Equal(atLocal("2026-11-02 18:00")) || len(saved.Upcoming) != 3 || saved.Created.IsZero() {
		t.Fatalf("bad saved view: %+v", saved)
	}
	if !c.automationsEnabled() {
		t.Fatal("saved automation not on")
	}
	if err := c.automationCall("automationToggle", automationRequest{ID: saved.ID, Enabled: false}, nil); err != nil {
		t.Fatal(err)
	}
	var list []automationView
	if err := c.automationCall("automationList", automationRequest{}, &list); err != nil || len(list) != 1 || list[0].Enabled || !list[0].Next.IsZero() {
		t.Fatalf("bad list after pausing: %+v %v", list, err)
	}
	// Turning it on later does not run the times it was off for.
	h.now = atLocal("2026-11-03 20:00")
	if err := c.automationCall("automationToggle", automationRequest{ID: saved.ID, Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	c.automationTick()
	if len(h.sent) != 0 {
		t.Fatal("times while paused were run")
	}
	if err := c.automationCall("automationRunNow", automationRequest{ID: saved.ID}, nil); err != nil || len(h.sent) != 1 {
		t.Fatalf("run now failed: %v", err)
	}
	if err := c.automationCall("automationRunNow", automationRequest{ID: saved.ID}, nil); err == nil {
		t.Fatal("second run started while one runs")
	}
	edit := list[0].Automation
	edit.Schedule = "0 7 * * *"
	if err := c.automationCall("automationSave", automationRequest{Automation: &edit}, &saved); err != nil || saved.ScheduleText != "每天 7:00" || len(saved.Runs) != 1 {
		t.Fatalf("edit lost runs or schedule: %+v %v", saved, err)
	}
	if err := c.automationCall("automationDelete", automationRequest{ID: saved.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.automationCall("automationList", automationRequest{}, &list); err != nil || len(list) != 0 || c.automationsEnabled() {
		t.Fatalf("delete failed: %+v %v", list, err)
	}
	bad := Automation{Name: "x", Project: c.cwd, Instructions: "y", Schedule: "nope"}
	if err := c.automationCall("automationSave", automationRequest{Automation: &bad}, nil); err == nil {
		t.Fatal("bad schedule saved")
	}
	if err := (&chatController{}).automationCall("automationList", automationRequest{}, nil); err == nil {
		t.Fatal("automations ran without their owner")
	}
	_ = time.Now
}

func TestServiceCallTimeoutsFitSlowOperations(t *testing.T) {
	if serviceCallTimeout("automationDraft") <= 2*time.Minute {
		t.Fatal("a draft is cut off before the agent's own limit")
	}
	if serviceCallTimeout("mcpAutomationCreate") <= miyaRequestTimeout || serviceCallTimeout("mcpAutomationUpdate") <= miyaRequestTimeout {
		t.Fatal("a confirmation card is cut off before the person answers")
	}
	if serviceCallTimeout("state") != 15*time.Second {
		t.Fatal("ordinary requests lost their timeout")
	}
}
