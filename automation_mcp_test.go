package main

import (
	"strings"
	"testing"
	"time"

	"miya/internal/automation"
	"miya/internal/codex"
)

// automationCaller is a conversation in the middle of a miya tool call.
func automationCaller(h *automationHarness, tool string) Thread {
	id := h.c.nextID()
	h.c.addThread("", Thread{ID: id, Agent: agentClaude, ServerID: "s-" + tool, Busy: true,
		Messages: []Message{{ID: h.c.nextID(), Role: "tool", Kind: "claudeTool", Status: "inProgress", Content: "mcp__miya__" + tool + "\n{}"}}})
	return h.c.threadWithID(id)
}

func startDeferred(t *testing.T, res any, err error) <-chan any {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	d, ok := res.(deferred)
	if !ok {
		t.Fatalf("not waiting for the user: %v", res)
	}
	out := make(chan any, 1)
	go func() {
		v, err := d()
		if err != nil {
			out <- err
			return
		}
		out <- v
	}()
	return out
}

func await(t *testing.T, out <-chan any) string {
	t.Helper()
	select {
	case v := <-out:
		s, ok := v.(string)
		if !ok {
			t.Fatalf("bad answer: %v", v)
		}
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
		return ""
	}
}

func miyaCard(t *testing.T, c *chatController) codex.Approval {
	t.Helper()
	for _, a := range c.approvals() {
		if isMiyaApproval(a) {
			return a
		}
	}
	t.Fatal("no confirmation card")
	return codex.Approval{}
}

func TestMCPCreateAutomationAsksFirst(t *testing.T) {
	h := newAutomationHarness(t)
	h.c.run = func(f func()) { f() }
	caller := automationCaller(h, "create_automation")
	args := &mcpAutomationArgs{Name: "每日分拣", Schedule: "0 9 * * 1-5", Instructions: "看 Issue", Check: "go test ./..."}
	res, err := h.c.mcpHandle("mcpAutomationCreate", mcpRequest{Tool: "create_automation", Auto: args})
	out := startDeferred(t, res, err)
	card := miyaCard(t, h.c)
	if card.ThreadID != caller.ServerID || requestAgent(card) != "Miya" || !strings.Contains(card.Command, "Weekdays 9:00") || !strings.Contains(card.Command, "Check: go test ./...; up to 3 rounds") || !h.c.threadAwaitsUser(caller) {
		t.Fatalf("bad card: %+v", card)
	}
	if len(h.s.Store.Automations) != 0 {
		t.Fatal("created before the user accepted")
	}
	h.c.respond(card, approvalResult(card, true))
	if got := await(t, out); !strings.Contains(got, "Created automation \"每日分拣\"") || !strings.Contains(got, "11/2 (Mon) 09:00") {
		t.Fatalf("bad answer: %q", got)
	}
	if len(h.s.Store.Automations) != 1 || len(h.c.approvals()) != 0 {
		t.Fatalf("not created: %+v", h.s.Store.Automations)
	}
	a := h.s.Store.Automations[0]
	if a.Project != h.c.cwd || a.Agent != agentClaude || a.Rounds != 3 || !a.Enabled {
		t.Fatalf("bad automation: %+v", a)
	}

	res, err = h.c.mcpHandle("mcpAutomationCreate", mcpRequest{Tool: "create_automation", Auto: args})
	out = startDeferred(t, res, err)
	card = miyaCard(t, h.c)
	h.c.respond(card, approvalResult(card, false))
	if got := await(t, out); !strings.Contains(got, "No automation was created") || len(h.s.Store.Automations) != 1 {
		t.Fatalf("declined card created one: %q", got)
	}

	if _, err := h.c.mcpHandle("mcpAutomationCreate", mcpRequest{Tool: "create_automation", Auto: &mcpAutomationArgs{Name: "x", Schedule: "每天", Instructions: "y"}}); err == nil || len(h.c.approvals()) != 0 {
		t.Fatalf("bad schedule reached the user: %v", err)
	}
	if _, err := h.c.mcpHandle("mcpAutomationCreate", mcpRequest{Tool: "update_automation", Auto: args}); err == nil || err.Error() != mcpCallerUnknown {
		t.Fatalf("unknown caller not reported: %v", err)
	}
}

func TestMCPUpdateAndListAutomations(t *testing.T) {
	h := newAutomationHarness(t)
	h.c.run = func(f func()) { f() }
	a := h.add(t, automation.Automation{Name: "每日分拣", Instructions: "看 Issue", Schedule: "0 9 * * 1-5"})
	off := false
	res, err := h.c.mcpHandle("mcpAutomationUpdate", mcpRequest{Tool: "update_automation", Auto: &mcpAutomationArgs{ID: a.ID, Enabled: &off}})
	if err != nil || !strings.Contains(res.(string), "Paused") || h.s.Store.Automations[0].Enabled || len(h.c.approvals()) != 0 {
		t.Fatalf("pausing: %v %v", res, err)
	}
	automationCaller(h, "update_automation")
	res, err = h.c.mcpHandle("mcpAutomationUpdate", mcpRequest{Tool: "update_automation", Auto: &mcpAutomationArgs{ID: "每日分拣", Schedule: "0 7 * * *"}})
	out := startDeferred(t, res, err)
	card := miyaCard(t, h.c)
	if !strings.Contains(card.Command, "Schedule: Weekdays 9:00 → Daily at 7:00") {
		t.Fatalf("bad diff: %q", card.Command)
	}
	h.c.respond(card, approvalResult(card, true))
	if got := await(t, out); !strings.Contains(got, "Updated") || h.s.Store.Automations[0].Schedule != "0 7 * * *" {
		t.Fatalf("not changed: %q %+v", got, h.s.Store.Automations[0])
	}
	list, err := h.c.mcpHandle("mcpAutomationList", mcpRequest{Tool: "list_automations"})
	if err != nil || !strings.Contains(list.(string), a.ID+"\"每日分拣\"") || !strings.Contains(list.(string), "Paused") {
		t.Fatalf("bad list: %v %v", list, err)
	}
	if _, err := h.c.mcpHandle("mcpAutomationUpdate", mcpRequest{Tool: "update_automation", Auto: &mcpAutomationArgs{ID: "nope"}}); err == nil {
		t.Fatal("unknown automation changed")
	}
}

// A conversation in a background project, reached through Codex's shared
// app-server whose miya server was started for /, creates its automation in
// its own project.
func TestMCPCreateAutomationFromBackgroundProject(t *testing.T) {
	h := newAutomationHarness(t)
	h.c.run = func(f func()) { f() }
	other := t.TempDir()
	h.c.projectCache = map[string][]Thread{h.c.cwd: h.c.threads(), other: {{ID: 77, Agent: agentCodex, ServerID: "cx", Busy: true,
		Messages: []Message{{ID: 78, Role: "tool", Kind: "mcpToolCall", Status: "inProgress", Item: &codex.Item{Server: "miya", Tool: "create_automation"}}}}}}
	res, err := h.c.mcpHandle("mcpAutomationCreate", mcpRequest{Workspace: "/", Tool: "create_automation", Auto: &mcpAutomationArgs{Name: "上游对账", Schedule: "0 8 * * *", Instructions: "对账"}})
	out := startDeferred(t, res, err)
	card := miyaCard(t, h.c)
	if card.ThreadID != "cx" {
		t.Fatalf("card not in the caller's conversation: %+v", card)
	}
	h.c.respond(card, approvalResult(card, true))
	if got := await(t, out); !strings.Contains(got, "Created") {
		t.Fatalf("bad answer: %q", got)
	}
	if a := h.s.Store.Automations[0]; a.Project != other || a.Agent != agentCodex {
		t.Fatalf("automation not in the caller's project: %+v", a)
	}
	list := h.c.mcpThreads(mcpRequest{Tool: "create_automation"})
	for _, th := range list.Threads {
		if th.ID == 77 {
			t.Fatal("the caller listed itself")
		}
	}
}
