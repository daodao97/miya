package automation

import (
	"strings"
	"testing"
	"time"
)

// A run of an automation given to a Bot is a conversation of the Bot: its
// agent, and the narrower of the two access levels as the Bot is now.
func TestRunsOfAnAutomationGivenToABot(t *testing.T) {
	w := newWakeHarness(t)
	botAccess := AccessRead
	bots := map[string]bool{"b1": true}
	w.rn.Bot = func(id string) (string, Access, bool) { return "claude-code", botAccess, bots[id] }
	a := ingressAutomation(WakePolicy{})
	a.Agent, a.Access, a.Bot = "codex", AccessFull, "b1"
	a = w.add(a)
	w.runIngress(a.ID, "ping", "x")
	sent := w.h.sent[len(w.h.sent)-1]
	if sent.Agent != "claude-code" || sent.Bot != "b1" || sent.Policy != AccessRead.Policy() {
		t.Fatalf("the run was sent as agent %q, Bot %q, policy %q", sent.Agent, sent.Bot, sent.Policy)
	}

	// The automation's own access holds when it is the narrower.
	botAccess = AccessFull
	w.rn.Store.Automations[0].Access = AccessEdit
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "y")
	if sent := w.h.sent[len(w.h.sent)-1]; sent.Policy != AccessEdit.Policy() {
		t.Fatalf("policy %q, want the automation's edit", sent.Policy)
	}

	// A deleted Bot fails the run instead of running it as someone else.
	delete(bots, "b1")
	w.now = w.now.Add(time.Minute)
	before := len(w.h.sent)
	_, _ = w.rn.Ingress(a.ID, "", "", false, "ping", "")
	if run := w.lastRun(a.ID); run.Result != Failed || !strings.Contains(run.Summary, "Bot") || len(w.h.sent) != before {
		t.Fatalf("a run for a deleted Bot: %+v, %d messages sent", run, len(w.h.sent)-before)
	}
}

// With NotifyNever no run notifies, and runs with news are not marked quiet:
// they still wait in what needs the person.
func TestNotifyNever(t *testing.T) {
	w := newWakeHarness(t)
	a := ingressAutomation(WakePolicy{})
	a.NotifyNever = true
	a = w.add(a)
	w.runIngress(a.ID, "ping", "#12 stale")
	if n := len(w.runNotices(a.ID)); n != 0 || w.lastRun(a.ID).Quiet {
		t.Fatalf("a run with a finding: %d notices, %+v", n, w.lastRun(a.ID))
	}
	w.now = w.now.Add(time.Minute)
	if _, err := w.rn.Ingress(a.ID, "", "", false, "ping", ""); err != nil {
		t.Fatal(err)
	}
	w.rn.Store.UpdateLastRun(a.ID, func(r *Run) {
		r.Report = &Report{Summary: "blocked", Findings: []string{"#13"}, NeedsUser: true}
	})
	w.endRun(a.ID)
	if n := len(w.runNotices(a.ID)); n != 0 || w.lastRun(a.ID).Quiet {
		t.Fatalf("a run that needs the person: %d notices, %+v", n, w.lastRun(a.ID))
	}
}

func TestNarrower(t *testing.T) {
	for _, tc := range []struct{ a, b, want Access }{
		{"", "", ""},
		{"", AccessRead, AccessRead},
		{AccessFull, "", AccessFull},
		{AccessFull, AccessRead, AccessRead},
		{AccessEdit, AccessExternal, AccessEdit},
		{AccessExternal, AccessEdit, AccessEdit},
	} {
		if got := Narrower(tc.a, tc.b); got != tc.want {
			t.Errorf("Narrower(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

// Runs that notify, even when kept off the phone, are posted in the chat of
// the Bot the automation is given to; quiet ones are not.
func TestRunsPostedToTheBot(t *testing.T) {
	w := newWakeHarness(t)
	w.rn.Bot = func(string) (string, Access, bool) { return "codex", "", true }
	var posted []string
	w.rn.Report = func(a Automation, run Run, title, body string, needsUser bool) { posted = append(posted, title) }
	a := ingressAutomation(WakePolicy{})
	a.Bot, a.NotifyNever = "b1", true
	a = w.add(a)
	w.runIngress(a.ID, "ping", "#12 stale")
	if len(posted) != 1 {
		t.Fatalf("a run with news posted %d reports", len(posted))
	}
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "#12 stale")
	if len(posted) != 1 {
		t.Fatalf("a run with nothing new posted: %q", posted)
	}
	plain := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(plain.ID, "ping", "news")
	if len(posted) != 1 {
		t.Fatalf("an automation given to no Bot posted: %q", posted)
	}
}
