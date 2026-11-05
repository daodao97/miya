package main

import (
	"miya/internal/automation"
	"testing"
	"time"
)

func TestAutomationOps(t *testing.T) {
	h := newAutomationHarness(t)
	c := h.c
	var saved automation.View
	draft := automation.Automation{Name: "日报", Project: c.cwd, Agent: agentCodex, Instructions: "写日报", Schedule: "0 18 * * *", Enabled: true}
	if err := c.automationCall("automationSave", automation.Request{Automation: &draft}, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.ScheduleText != "Daily at 18:00" || !saved.Next.Equal(atLocal("2026-11-02 18:00")) || len(saved.Upcoming) != 3 || saved.Created.IsZero() {
		t.Fatalf("bad saved view: %+v", saved)
	}
	if !c.auto.Enabled() {
		t.Fatal("saved automation not on")
	}
	if err := c.automationCall("automationToggle", automation.Request{ID: saved.ID, Enabled: false}, nil); err != nil {
		t.Fatal(err)
	}
	var list []automation.View
	if err := c.automationCall("automationList", automation.Request{}, &list); err != nil || len(list) != 1 || list[0].Enabled || !list[0].Next.IsZero() {
		t.Fatalf("bad list after pausing: %+v %v", list, err)
	}
	// Turning it on later does not run the times it was off for.
	h.now = atLocal("2026-11-03 20:00")
	if err := c.automationCall("automationToggle", automation.Request{ID: saved.ID, Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	c.auto.Tick()
	if len(h.sent) != 0 {
		t.Fatal("times while paused were run")
	}
	if err := c.automationCall("automationRunNow", automation.Request{ID: saved.ID}, nil); err != nil || len(h.sent) != 1 {
		t.Fatalf("run now failed: %v", err)
	}
	if err := c.automationCall("automationRunNow", automation.Request{ID: saved.ID}, nil); err == nil {
		t.Fatal("second run started while one runs")
	}
	edit := list[0].Automation
	edit.Schedule = "0 7 * * *"
	if err := c.automationCall("automationSave", automation.Request{Automation: &edit}, &saved); err != nil || saved.ScheduleText != "Daily at 7:00" || len(saved.Runs) != 1 {
		t.Fatalf("edit lost runs or schedule: %+v %v", saved, err)
	}
	if err := c.automationCall("automationDelete", automation.Request{ID: saved.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.automationCall("automationList", automation.Request{}, &list); err != nil || len(list) != 0 || c.auto.Enabled() {
		t.Fatalf("delete failed: %+v %v", list, err)
	}
	bad := automation.Automation{Name: "x", Project: c.cwd, Instructions: "y", Schedule: "nope"}
	if err := c.automationCall("automationSave", automation.Request{Automation: &bad}, nil); err == nil {
		t.Fatal("bad schedule saved")
	}
	if err := (&chatController{}).automationCall("automationList", automation.Request{}, nil); err == nil {
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
