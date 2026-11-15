package automation

import (
	"strings"
	"testing"
	"time"

	"miya/internal/project/worktree"
)

func TestFoundNothingNewComparesWhatWasFound(t *testing.T) {
	prev := &Report{Summary: "Two issues open", Findings: []string{"#12 stale", "#9 needs repro"}, Metrics: []Metric{{Name: "open", Value: "2"}}}
	same := Report{Summary: "Still two open issues", Findings: []string{"#9 needs repro", " #12 stale "}, Metrics: []Metric{{Name: "open", Value: "2"}}}
	cases := []struct {
		name string
		run  Run
		prev *Report
		want bool
	}{
		{"same findings, other words", Run{Result: Passed, Report: &same}, prev, true},
		{"a metric moved", Run{Result: Passed, Report: &Report{Findings: same.Findings, Metrics: []Metric{{Name: "open", Value: "3"}}}}, prev, false},
		{"a finding is new", Run{Result: Passed, Report: &Report{Findings: append([]string{"#14 new"}, same.Findings...), Metrics: same.Metrics}}, prev, false},
		{"needs the person", Run{Result: Passed, Report: &Report{Findings: same.Findings, Metrics: same.Metrics, NeedsUser: true}}, prev, false},
		{"made by Miya", Run{Result: Passed, Report: &Report{Findings: same.Findings, Metrics: same.Metrics, Auto: true}}, prev, false},
		{"failed", Run{Result: Failed, Report: &same}, prev, false},
		{"nothing before", Run{Result: Passed, Report: &same}, nil, false},
		{"no result", Run{Result: Passed}, prev, false},
	}
	for _, tc := range cases {
		if got := foundNothingNew(tc.run, tc.prev); got != tc.want {
			t.Fatalf("%s: got %v", tc.name, got)
		}
	}
}

// runIngress starts a run of a with an ingress wake that brings data,
// lets it report findings, and ends it.
func (w *wakeHarness) runIngress(id, data string, findings ...string) {
	w.t.Helper()
	wk, err := w.rn.Ingress(id, "", "", false, data, "")
	if err != nil || wk.Outcome != WakeStarted {
		w.t.Fatalf("not started: %+v %v", wk, err)
	}
	w.rn.Store.UpdateLastRun(id, func(r *Run) { r.Report = &Report{Summary: "checked", Findings: findings} })
	w.endRun(id)
}

func (w *wakeHarness) lastRun(id string) Run {
	runs := w.rn.Store.Runs[id]
	return runs[len(runs)-1]
}

// Wakes that bring nothing new wait longer after triggered runs that found
// nothing new; wakes that bring data still start runs.
func TestTriggeredRunsBackOffWhenNothingIsNew(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(a.ID, "", "#12 stale")
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "", "#12 stale")
	if !w.lastRun(a.ID).Unchanged {
		t.Fatalf("the second run with the same findings: %+v", w.lastRun(a.ID))
	}
	w.now = w.now.Add(time.Minute)
	// One run alike is not enough to back off.
	w.runIngress(a.ID, "", "#12 stale")
	last := w.now
	w.now = w.now.Add(5 * time.Minute)
	if wk, _ := w.rn.Ingress(a.ID, "", "", false, "", ""); wk.Outcome != WakeHeld || !strings.Contains(wk.Detail, "Backing off") || !strings.Contains(wk.Detail, "last 2") {
		t.Fatalf("no backoff after two runs alike: %+v", wk)
	}
	// Data is something new to look at.
	w.runIngress(a.ID, "issue #15 opened", "#12 stale")
	last = w.now
	// Three alike in a row: the wait doubles from 10 minutes to 20.
	w.now = last.Add(15 * time.Minute)
	if wk, _ := w.rn.Ingress(a.ID, "", "", false, "", ""); wk.Outcome != WakeHeld {
		t.Fatalf("the wait did not grow: %+v", wk)
	}
	w.now = last.Add(21 * time.Minute)
	w.runIngress(a.ID, "", "#12 stale", "#16 new")
	if w.lastRun(a.ID).Unchanged {
		t.Fatal("a run with a new finding counted as nothing new")
	}
	// Something new ends the backoff.
	w.now = w.now.Add(time.Minute)
	if wk, _ := w.rn.Ingress(a.ID, "", "", false, "", ""); wk.Outcome != WakeStarted {
		t.Fatalf("still backing off after a change: %+v", wk)
	}
}

// StallAfter runs in a row that find nothing new stop triggered runs, with
// one notice, until the person edits the automation or runs it now.
func TestStallStopsTriggeredRunsUntilAPersonLooks(t *testing.T) {
	w := newWakeHarness(t)
	auto := ingressAutomation(WakePolicy{})
	auto.StallAfter = 3
	a := w.add(auto)
	for i := 0; i < 4; i++ {
		w.now = w.now.Add(time.Minute)
		w.runIngress(a.ID, "ping", "CI still red: TestFlaky")
	}
	if _, stalled := w.rn.Store.Stalled[a.ID]; !stalled {
		t.Fatalf("not stalled after 3 runs alike: %+v", w.rn.Store.Runs[a.ID])
	}
	stalls := 0
	for _, n := range w.h.notices {
		if strings.HasPrefix(n.ID, "miya-auto-stall-") {
			stalls++
		}
	}
	if stalls != 1 {
		t.Fatalf("stall notices: %d", stalls)
	}
	if v := w.rn.View(a, w.now); v.Stalled.IsZero() {
		t.Fatal("the view does not show the stall")
	}
	w.now = w.now.Add(time.Minute)
	if wk, _ := w.rn.Ingress(a.ID, "", "", false, "ping", ""); wk.Outcome != WakeHeld || !strings.Contains(wk.Detail, "Stalled") {
		t.Fatalf("a stalled automation started a triggered run: %+v", wk)
	}
	// Run now is a person looking.
	if wk, err := w.rn.Wake(WakeEvent{Automation: a.ID, Source: SourceManual}); err != nil || wk.Outcome != WakeStarted {
		t.Fatalf("run now: %+v %v", wk, err)
	}
	if _, stalled := w.rn.Store.Stalled[a.ID]; stalled {
		t.Fatal("run now did not end the stall")
	}
	w.endRun(a.ID)
	// Editing it ends a stall too.
	w.rn.Store.Stalled = map[string]time.Time{a.ID: w.now}
	edited := w.rn.Store.Automations[0]
	edited.Instructions = "Fix the flaky test, or skip it with a note"
	if _, err := w.rn.Save(edited); err != nil {
		t.Fatal(err)
	}
	if _, stalled := w.rn.Store.Stalled[a.ID]; stalled {
		t.Fatal("editing did not end the stall")
	}
	// Without StallAfter, runs alike never stall it.
	plain := w.add(ingressAutomation(WakePolicy{}))
	for i := 0; i < 5; i++ {
		w.now = w.now.Add(time.Minute)
		w.runIngress(plain.ID, "ping", "same")
	}
	if _, stalled := w.rn.Store.Stalled[plain.ID]; stalled {
		t.Fatal("stalled without StallAfter")
	}
}

// runNotices are the notices of a's runs.
func (w *wakeHarness) runNotices(id string) []string {
	var titles []string
	for _, n := range w.h.notices {
		if strings.HasPrefix(n.ID, "miya-auto-"+id+"-") {
			titles = append(titles, n.Title)
		}
	}
	return titles
}

// A run that passes with nothing new stays in the history without a
// notice; one that needs the person, or any run of an automation that asks
// for every run, still notifies.
func TestRunsWithNothingNewAreQuiet(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(a.ID, "ping", "#12 stale")
	if n := len(w.runNotices(a.ID)); n != 1 || w.lastRun(a.ID).Quiet {
		t.Fatalf("the first run with a finding: %d notices, %+v", n, w.lastRun(a.ID))
	}
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "#12 stale")
	if n := len(w.runNotices(a.ID)); n != 1 || !w.lastRun(a.ID).Quiet {
		t.Fatalf("a run with nothing new: %d notices, %+v", n, w.lastRun(a.ID))
	}
	// The same findings, but now it needs the person.
	w.now = w.now.Add(time.Minute)
	if wk, err := w.rn.Ingress(a.ID, "", "", false, "ping", ""); err != nil || wk.Outcome != WakeStarted {
		t.Fatalf("%+v %v", wk, err)
	}
	w.rn.Store.UpdateLastRun(a.ID, func(r *Run) {
		r.Report = &Report{Summary: "blocked", Findings: []string{"#12 stale"}, NeedsUser: true}
	})
	w.endRun(a.ID)
	if titles := w.runNotices(a.ID); len(titles) != 2 || !strings.Contains(titles[1], "Needs your attention") || w.lastRun(a.ID).Quiet {
		t.Fatalf("a run that needs the person: %q", titles)
	}

	// A result with nothing in it is quiet from the first run.
	empty := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(empty.ID, "ping")
	if n := len(w.runNotices(empty.ID)); n != 0 || !w.lastRun(empty.ID).Quiet {
		t.Fatalf("an empty result: %d notices", n)
	}
	// Without a result of its own the run is not judged quiet.
	w.now = w.now.Add(time.Minute)
	if _, err := w.rn.Ingress(empty.ID, "", "", false, "ping", ""); err != nil {
		t.Fatal(err)
	}
	w.endRun(empty.ID)
	if n := len(w.runNotices(empty.ID)); n != 1 {
		t.Fatalf("a run without a result: %d notices", n)
	}

	every := ingressAutomation(WakePolicy{})
	every.NotifyEveryRun = true
	loud := w.add(every)
	for i := 0; i < 3; i++ {
		w.now = w.now.Add(time.Minute)
		w.runIngress(loud.ID, "ping", "same")
	}
	if n := len(w.runNotices(loud.ID)); n != 3 {
		t.Fatalf("notify every run: %d notices", n)
	}
}

func TestQuietRunRules(t *testing.T) {
	same := &Report{Summary: "ok", Findings: []string{"x"}}
	kept := &worktree.Worktree{Dir: "/tmp/wt"}
	cases := []struct {
		name string
		a    Automation
		run  Run
		want bool
	}{
		{"unchanged", Automation{}, Run{Result: Passed, Report: same, Unchanged: true}, true},
		{"failed though unchanged", Automation{}, Run{Result: Failed, Report: same, Unchanged: true}, false},
		{"interrupted", Automation{}, Run{Result: Interrupted, Report: &Report{Summary: "stopped"}}, false},
		{"empty result made by Miya", Automation{}, Run{Result: Passed, Report: &Report{Summary: "done", Auto: true}}, false},
		{"changes wait in a worktree", Automation{}, Run{Result: Passed, Report: same, Unchanged: true, Worktree: kept, WorktreeState: WorktreeKept, Changes: worktree.Changes{Commits: 1}}, false},
		{"worktree cleaned", Automation{}, Run{Result: Passed, Report: same, Unchanged: true, Worktree: kept, WorktreeState: WorktreeCleaned}, true},
		{"every run", Automation{NotifyEveryRun: true}, Run{Result: Passed, Report: same, Unchanged: true}, false},
	}
	for _, tc := range cases {
		if got := quietRun(tc.a, tc.run); got != tc.want {
			t.Fatalf("%s: got %v", tc.name, got)
		}
	}
}

// A run after quiet ones is told what the person last heard, and not
// again once the previous run itself notified.
func TestRunAfterQuietRunsIsToldWhatWasReported(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(a.ID, "ping", "#12 stale")
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "#12 stale")
	if !w.lastRun(a.ID).Quiet {
		t.Fatal("the second run was not quiet")
	}
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "#12 stale", "#16 new")
	last := w.h.sent[len(w.h.sent)-1].Text
	if !strings.Contains(last, "last notified") || !strings.Contains(last, "#12 stale") {
		t.Fatalf("the run after a quiet one was not told what was reported:\n%s", last)
	}
	// The run before this one notified: its result is already the previous
	// result, and nothing more is added.
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "#16 new")
	if last := w.h.sent[len(w.h.sent)-1].Text; strings.Contains(last, "last notified") {
		t.Fatalf("told again although the previous run notified:\n%s", last)
	}
}

// A run's verdict comes from the check command Miya ran, not from what the
// agent reported.
func TestRunVerdictComesFromTheCheck(t *testing.T) {
	w := newWakeHarness(t)
	pass := true
	w.rn.Check = func(string, string) (bool, string) { return pass, "FAIL TestFlaky" }
	checked := ingressAutomation(WakePolicy{})
	checked.Check, checked.Rounds = "go test ./...", 1
	a := w.add(checked)
	w.runIngress(a.ID, "ping", "all green")
	if r := w.lastRun(a.ID); r.Result != Passed || r.Verdict != VerdictVerified {
		t.Fatalf("check passed: %+v", r)
	}
	// The agent says it is done; the check says otherwise.
	pass = false
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "all green")
	if r := w.lastRun(a.ID); r.Result != Failed || r.Verdict != VerdictCheckFailed {
		t.Fatalf("check failed: %+v", r)
	}
	plain := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(plain.ID, "ping", "done")
	if r := w.lastRun(plain.ID); r.Result != Passed || r.Verdict != VerdictUnchecked || VerdictLabel(r.Verdict) == "" {
		t.Fatalf("no check: %+v", r)
	}
}
