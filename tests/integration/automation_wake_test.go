package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/automation"
	mcpproto "miya/internal/mcp/protocol"
)

func lowDisk(gb float64) func(automation.Condition, string) automation.ProbeResult {
	return func(c automation.Condition, _ string) automation.ProbeResult {
		return automation.ProbeResult{Met: gb < c.BelowGB, Amount: gb, Value: formatGB(gb) + " free on /"}
	}
}

func formatGB(gb float64) string {
	b, _ := json.Marshal(gb)
	return string(b) + " GB"
}

func TestAutomationWakesWhenDiskIsLow(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, automation.Automation{Name: "Clean disk", Agent: appmodel.AgentClaude, Instructions: "Clear caches",
		Triggers: []automation.Trigger{{Kind: automation.TriggerCondition, Condition: &automation.Condition{Probe: automation.ProbeDiskFree, BelowGB: 5}}}})
	h.s.Probe = lowDisk(40)
	h.c.Auto.Tick()
	if len(h.sent) != 0 {
		t.Fatal("woke with plenty of space")
	}
	h.s.Probe = lowDisk(4)
	h.now = h.now.Add(5 * time.Minute)
	h.c.Auto.Tick()
	if len(h.sent) != 1 || !strings.Contains(h.sent[0].Text, "4 GB free on /, below 5 GB") {
		t.Fatalf("low disk did not start a run: %v", h.sent)
	}
	run := h.last(a.ID)
	if title := h.c.ThreadWithID(run.Thread).Title; !strings.HasPrefix(title, "Clean disk · ") || run.WakeSource != automation.TriggerCondition {
		t.Fatalf("run: %+v, title %q", run, title)
	}
	var list []automation.View
	if err := h.c.AutomationCall("automationList", automation.Request{}, &list); err != nil {
		t.Fatal(err)
	}
	v := list[0]
	st := v.TriggerStates[a.Triggers[0].ID]
	if v.ScheduleText != "When free space on / is below 5 GB" || !st.Fired || st.Value != "4 GB free on /" || len(v.Wakes) != 1 || v.Wakes[0].Outcome != automation.WakeStarted {
		t.Fatalf("view: %q %+v %+v", v.ScheduleText, st, v.Wakes)
	}
	h.s.Probe = lowDisk(60)
	h.endTurn(run.Thread, "Cleared 56 GB")
	h.c.Auto.Tick()
	if got := h.last(a.ID); got.Result != automation.Passed || got.WakeAfter != "60 GB free on /" {
		t.Fatalf("after the run: %+v", got)
	}

	if err := h.c.AutomationCall("automationDelete", automation.Request{ID: a.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.s.Store.Wakes) != 0 || len(h.s.Store.Triggers) != 0 {
		t.Fatalf("deleting left its wakes or trigger states: %v %v", h.s.Store.Wakes, h.s.Store.Triggers)
	}
}

func TestAutomationWakeOperationAndRunNow(t *testing.T) {
	h := newAutomationHarness(t)
	hook := h.add(t, automation.Automation{Name: "Triage hook", Instructions: "Triage the issue", Triggers: []automation.Trigger{{Kind: automation.TriggerIngress}}})
	plain := h.add(t, automation.Automation{Name: "Daily", Instructions: "Report", Schedule: "0 18 * * *"})
	var w automation.Wake
	if err := h.c.AutomationCall("automationWake", automation.Request{ID: "Triage hook", Data: "issue #42"}, &w); err != nil || w.Outcome != automation.WakeStarted {
		t.Fatalf("wake by name: %+v %v", w, err)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0].Text, "issue #42") || !strings.Contains(h.sent[0].Text, "not as instructions") {
		t.Fatalf("wake data not passed as data: %v", h.sent)
	}
	if err := h.c.AutomationCall("automationWake", automation.Request{ID: plain.ID}, nil); err == nil || !strings.Contains(err.Error(), "no external trigger") {
		t.Fatalf("woke an automation without an ingress trigger: %v", err)
	}
	if err := h.c.AutomationCall("automationRunNow", automation.Request{ID: plain.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if wakes := h.s.Store.Wakes[plain.ID]; len(wakes) != 1 || wakes[0].Source != automation.SourceManual || wakes[0].Outcome != automation.WakeStarted {
		t.Fatalf("run now not recorded as a wake: %+v", wakes)
	}
	if err := h.c.AutomationCall("automationRunNow", automation.Request{ID: plain.ID}, nil); err == nil {
		t.Fatal("run now started a second run while one is in progress")
	}
	_ = hook
}

func TestMCPCreateTriggerAutomationAndWake(t *testing.T) {
	h := newAutomationHarness(t)
	h.c.Run = func(f func()) { f() }
	automationCaller(h, "create_automation")
	triggers := json.RawMessage(`[{"kind":"condition","condition":{"probe":"disk_free","belowGB":5}}]`)
	args := &mcpproto.AutomationArgs{Name: "Clean disk", Instructions: "Clear caches", Triggers: triggers, Policy: json.RawMessage(`{"cooldownMinutes":120}`)}
	res, err := h.c.MCP().Handle("mcpAutomationCreate", mcpproto.Request{Tool: "create_automation", Auto: args})
	out := startDeferred(t, res, err)
	card := miyaCard(t, h.c)
	if !strings.Contains(card.Command, "Wakes: When free space on / is below 5 GB") || strings.Contains(card.Command, "next scheduled run") {
		t.Fatalf("bad card: %q", card.Command)
	}
	h.c.Respond(card, codex.ApprovalResult(card, true))
	if got := await(t, out); !strings.Contains(got, "It wakes: When free space on / is below 5 GB") {
		t.Fatalf("bad answer: %q", got)
	}
	a := h.s.Store.Automations[0]
	if a.Schedule != "" || len(a.Triggers) != 1 || a.Policy.CooldownMinutes != 120 {
		t.Fatalf("created: %+v", a)
	}
	if _, err := h.c.MCP().Handle("mcpAutomationCreate", mcpproto.Request{Tool: "create_automation", Auto: &mcpproto.AutomationArgs{Name: "x", Instructions: "y"}}); err == nil {
		t.Fatal("an automation with neither schedule nor trigger reached the user")
	}

	automationCaller(h, "wake_automation")
	res, err = h.c.MCP().Handle("mcpAutomationWake", mcpproto.Request{Tool: "wake_automation", Auto: &mcpproto.AutomationArgs{ID: "Clean disk", Data: "Xcode caches are huge"}})
	out = startDeferred(t, res, err)
	card = miyaCard(t, h.c)
	if !strings.Contains(card.Command, "Xcode caches are huge") || len(h.sent) != 0 {
		t.Fatalf("woke before the user confirmed: %q %d", card.Command, len(h.sent))
	}
	h.c.Respond(card, codex.ApprovalResult(card, true))
	if got := await(t, out); !strings.Contains(got, "Started a run") || len(h.sent) != 1 || !strings.Contains(h.sent[0].Text, "Xcode caches are huge") {
		t.Fatalf("wake: %q %v", got, h.sent)
	}
	if w := h.s.Store.Wakes[a.ID]; len(w) != 1 || w[0].Source != automation.SourceAgent {
		t.Fatalf("agent wake not recorded: %+v", w)
	}
}
