package integration

import (
	"encoding/json"
	svcproto "miya/internal/service/protocol"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/controller"
	svcclient "miya/internal/service/client"
)

func TestAutomationOps(t *testing.T) {
	h := newAutomationHarness(t)
	c := h.c
	var saved automation.View
	draft := automation.Automation{Name: "日报", Project: c.Cwd, Agent: appmodel.AgentCodex, Instructions: "写日报", Schedule: "0 18 * * *", Enabled: true}
	if err := c.AutomationCall("automationSave", automation.Request{Automation: &draft}, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.ScheduleText != "Daily at 18:00" || !saved.Next.Equal(atLocal("2026-11-02 18:00")) || len(saved.Upcoming) != 3 || saved.Created.IsZero() {
		t.Fatalf("bad saved view: %+v", saved)
	}
	if !c.Auto.Enabled() {
		t.Fatal("saved automation not on")
	}
	if err := c.AutomationCall("automationToggle", automation.Request{ID: saved.ID, Enabled: false}, nil); err != nil {
		t.Fatal(err)
	}
	var list []automation.View
	if err := c.AutomationCall("automationList", automation.Request{}, &list); err != nil || len(list) != 1 || list[0].Enabled || !list[0].Next.IsZero() {
		t.Fatalf("bad list after pausing: %+v %v", list, err)
	}
	// Turning it on later does not run the times it was off for.
	h.now = atLocal("2026-11-03 20:00")
	if err := c.AutomationCall("automationToggle", automation.Request{ID: saved.ID, Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	c.Auto.Tick()
	if len(h.sent) != 0 {
		t.Fatal("times while paused were run")
	}
	if err := c.AutomationCall("automationRunNow", automation.Request{ID: saved.ID}, nil); err != nil || len(h.sent) != 1 {
		t.Fatalf("run now failed: %v", err)
	}
	if err := c.AutomationCall("automationRunNow", automation.Request{ID: saved.ID}, nil); err == nil {
		t.Fatal("second run started while one runs")
	}
	edit := list[0].Automation
	edit.Schedule = "0 7 * * *"
	if err := c.AutomationCall("automationSave", automation.Request{Automation: &edit}, &saved); err != nil || saved.ScheduleText != "Daily at 7:00" || len(saved.Runs) != 1 {
		t.Fatalf("edit lost runs or schedule: %+v %v", saved, err)
	}
	if err := c.AutomationCall("automationDelete", automation.Request{ID: saved.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.AutomationCall("automationList", automation.Request{}, &list); err != nil || len(list) != 0 || c.Auto.Enabled() {
		t.Fatalf("delete failed: %+v %v", list, err)
	}
	bad := automation.Automation{Name: "x", Project: c.Cwd, Instructions: "y", Schedule: "nope"}
	if err := c.AutomationCall("automationSave", automation.Request{Automation: &bad}, nil); err == nil {
		t.Fatal("bad schedule saved")
	}
	if err := (&controller.Controller{}).AutomationCall("automationList", automation.Request{}, nil); err == nil {
		t.Fatal("automations ran without their owner")
	}
	_ = time.Now
}

func TestServiceCallTimeoutsFitSlowOperations(t *testing.T) {
	if svcclient.CallTimeout("automationDraft") <= 2*time.Minute {
		t.Fatal("a draft is cut off before the agent's own limit")
	}
	if svcclient.CallTimeout("mcpAutomationCreate") <= svcclient.CardTimeout || svcclient.CallTimeout("mcpAutomationUpdate") <= svcclient.CardTimeout {
		t.Fatal("a confirmation card is cut off before the person answers")
	}
	if svcclient.CallTimeout("state") != 15*time.Second {
		t.Fatal("ordinary requests lost their timeout")
	}
}

func TestSharedWebhookServiceOperationWithoutTasks(t *testing.T) {
	h := newAutomationHarness(t)
	value, err := h.c.ServiceRequest(svcproto.Request{Op: "automationWebhook"})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := value.(automation.WebhookView)
	if !ok || !v.Enabled {
		t.Fatal("service did not expose the global receiver before tasks exist")
	}
	h.s.ReceiveWebhook("notification before any task")
	value, err = h.c.ServiceRequest(svcproto.Request{Op: "automationWebhook"})
	if err != nil || len(value.(automation.WebhookView).Messages) != 1 {
		t.Fatal("received message not exposed by service")
	}
	args, _ := json.Marshal(automation.Request{Action: "disable"})
	value, err = h.c.ServiceRequest(svcproto.Request{Op: "automationWebhook", Args: args})
	if err != nil || value.(automation.WebhookView).Enabled || !h.s.Store.WebhookDisabled {
		t.Fatal("global receiver switch did not persist")
	}
}
