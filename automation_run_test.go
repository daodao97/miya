package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/codex"
)

type automationHarness struct {
	c      *chatController
	s      *automationState
	now    time.Time
	sent   []SendRequest
	checks []bool
}

// newAutomationHarness is a controller running automations on a test clock,
// whose submissions make conversations without an agent and whose checks
// pass or fail as listed.
func newAutomationHarness(t *testing.T) *automationHarness {
	h := &automationHarness{c: testController(), now: atLocal("2026-11-02 08:59")}
	c := h.c
	c.cwd = t.TempDir()
	c.startAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	h.s = c.auto
	h.s.now = func() time.Time { return h.now }
	h.s.background = func(work func() func()) { work()() }
	h.s.check = func(dir, command string) (bool, string) {
		ok := len(h.checks) > 0 && h.checks[0]
		if len(h.checks) > 0 {
			h.checks = h.checks[1:]
		}
		if ok {
			return true, "ok"
		}
		return false, "--- FAIL: TestX\nFAIL demo 0.01s"
	}
	h.s.submit = func(r SendRequest) int {
		h.sent = append(h.sent, r)
		if r.ThreadID != 0 {
			c.update(r.ThreadID, func(th *Thread) {
				th.Busy = true
				th.Messages = append(th.Messages, Message{ID: c.nextID(), Role: "user", Content: r.Text})
			})
			return r.ThreadID
		}
		id := c.nextID()
		c.addThread(r.Project, Thread{ID: id, Agent: r.Agent, Busy: true, Messages: []Message{{ID: c.nextID(), Role: "user", Content: r.Text}}})
		return id
	}
	return h
}

func (h *automationHarness) add(t *testing.T, a Automation) Automation {
	if a.Project == "" {
		a.Project = h.c.cwd
	}
	a.Enabled, a.Created = true, h.now
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	h.s.store.Automations = append(h.s.store.Automations, a)
	return a
}

func (h *automationHarness) endTurn(thread int, reply string) {
	h.c.update(thread, func(th *Thread) {
		th.Busy = false
		th.Messages = append(th.Messages, Message{ID: h.c.nextID(), Role: "assistant", Content: reply})
	})
}

func (h *automationHarness) last(id string) AutomationRun {
	r, _ := h.s.store.lastRun(id)
	return r
}

func TestAutomationRunsOnScheduleAndRetriesFailedChecks(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, Automation{Name: "Issue 分拣", Agent: agentClaude, Instructions: "看 Issue", Schedule: "0 9 * * 1-5", Check: "make test", Rounds: 3})
	h.c.automationTick()
	if len(h.sent) != 0 {
		t.Fatal("ran before its time")
	}
	h.now = atLocal("2026-11-02 09:00").Add(20 * time.Second)
	h.c.automationTick()
	if len(h.sent) != 1 || h.sent[0].Project != h.c.cwd || h.sent[0].Agent != agentClaude || !strings.HasPrefix(h.sent[0].Text, "看 Issue") {
		t.Fatalf("bad first round: %+v", h.sent)
	}
	run := h.last(a.ID)
	thread := h.c.threadWithID(run.Thread)
	if run.Result != runRunning || run.CatchUp || thread.Automation != a.ID || !strings.HasPrefix(thread.Title, "Issue 分拣 · Nov 2") {
		t.Fatalf("bad run start: %+v %+v", run, thread)
	}
	h.c.automationTick()
	if len(h.sent) != 1 {
		t.Fatal("acted while the round ran")
	}
	h.checks = []bool{false, true}
	h.endTurn(run.Thread, "修了 #142")
	h.c.automationTick()
	if len(h.sent) != 2 || h.sent[1].ThreadID != run.Thread || !strings.Contains(h.sent[1].Text, "Check failed (round 1/3)") || !strings.Contains(h.sent[1].Text, "FAIL: TestX") {
		t.Fatalf("failed check did not start round 2: %+v", h.sent)
	}
	if r := h.last(a.ID); r.Round != 2 || r.Result != runRunning {
		t.Fatalf("round not recorded: %+v", r)
	}
	h.endTurn(run.Thread, "测试修好了\n细节")
	h.c.automationTick()
	r := h.last(a.ID)
	if r.Result != runPassed || r.Round != 2 || r.Summary != "测试修好了" || r.Finished.IsZero() || len(h.s.active) != 0 {
		t.Fatalf("run not passed: %+v", r)
	}
	saved := loadAutomations(h.s.path)
	if len(saved.Runs[a.ID]) != 1 || saved.Automations[0].Due.IsZero() {
		t.Fatalf("run not saved: %+v", saved)
	}
}

func TestAutomationFailsWhenRoundsRunOut(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, Automation{Name: "CI", Instructions: "修 CI", Schedule: "* * * * *", Check: "make ci", Rounds: 2})
	h.now = h.now.Add(time.Minute)
	h.c.automationTick()
	thread := h.last(a.ID).Thread
	h.endTurn(thread, "试一次")
	h.c.automationTick()
	h.endTurn(thread, "再试一次")
	h.c.automationTick()
	if r := h.last(a.ID); r.Result != runFailed || r.Round != 2 || r.Summary != "Check failed: FAIL demo 0.01s" {
		t.Fatalf("bad failed run: %+v", r)
	}
}

func TestAutomationSkipsCatchesUpAndStops(t *testing.T) {
	h := newAutomationHarness(t)
	h.now = atLocal("2026-11-02 08:00")
	a := h.add(t, Automation{Name: "日报", Instructions: "写日报", Schedule: "0 * * * *"})
	h.now = atLocal("2026-11-02 11:30")
	h.c.automationTick()
	if len(h.sent) != 1 || !h.last(a.ID).CatchUp {
		t.Fatalf("missed hours did not make one catch-up run: %d sent, %+v", len(h.sent), h.last(a.ID))
	}
	thread := h.last(a.ID).Thread
	h.now = atLocal("2026-11-02 12:00").Add(10 * time.Second)
	h.c.automationTick()
	runs := h.s.store.Runs[a.ID]
	if len(h.sent) != 1 || len(runs) != 2 || runs[1].Result != runSkipped {
		t.Fatalf("busy run not skipped: %+v", runs)
	}
	// The run ended while skipped; its record is the one before the skip.
	h.s.store.Automations[0].Enabled = false
	h.endTurn(thread, "写好了")
	h.c.automationTick()
	if len(h.s.active) != 0 {
		t.Fatal("paused run still followed")
	}
	h.now = atLocal("2026-11-02 13:00").Add(10 * time.Second)
	h.c.automationTick()
	if len(h.sent) != 1 {
		t.Fatal("paused automation ran")
	}
	if h.c.automationsEnabled() {
		t.Fatal("paused automation keeps the service up")
	}
}

func TestAutomationWithoutProjectFails(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, Automation{Name: "x", Project: "/nonexistent/miya-test", Instructions: "y", Schedule: "* * * * *"})
	h.now = h.now.Add(time.Minute)
	h.c.automationTick()
	if r := h.last(a.ID); r.Result != runFailed || !strings.Contains(r.Summary, "Project directory does not exist") || len(h.sent) != 0 {
		t.Fatalf("missing project not reported: %+v", r)
	}
}

func TestAutomationWaitsForApprovals(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, Automation{Name: "x", Instructions: "y", Schedule: "* * * * *"})
	h.now = h.now.Add(time.Minute)
	h.c.automationTick()
	thread := h.last(a.ID).Thread
	id, _ := json.Marshal("claude:" + itoa(thread) + ":req1")
	h.c.setApprovals([]codex.Approval{{ID: id, Method: "claude/permission"}})
	h.endTurn(thread, "Waiting")
	h.c.automationTick()
	if h.last(a.ID).Result != runRunning {
		t.Fatal("a round waiting for approval was taken as ended")
	}
	h.c.setApprovals(nil)
	h.c.automationTick()
	if h.last(a.ID).Result != runPassed {
		t.Fatalf("run not finished after the approval: %+v", h.last(a.ID))
	}
}

func TestAutomationRunsInterruptedByRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automations.json")
	s := automationStore{Automations: []Automation{{ID: "a1", Name: "x", Project: "/ws", Agent: agentCodex, Instructions: "y", Schedule: "* * * * *", Rounds: 1}}}
	s.addRun("a1", AutomationRun{Result: runRunning})
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	c := testController()
	c.startAutomations(path, false)
	if r, _ := c.auto.store.lastRun("a1"); r.Result != runInterrupted {
		t.Fatalf("unfinished run kept running: %+v", r)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
