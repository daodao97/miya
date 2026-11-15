package automation

import (
	"testing"
	"time"

	"miya/internal/project/worktree"
)

func TestNeedsAttentionListsWhatWaitsForThePerson(t *testing.T) {
	now := atLocal("2026-11-15 10:00")
	ran := func(result string, ago time.Duration) Run {
		return Run{Started: now.Add(-ago - time.Minute), Finished: now.Add(-ago), Result: result, Summary: result}
	}
	needs := ran(Passed, time.Hour)
	needs.Report = &Report{Summary: "blocked", NeedsUser: true}
	merge := ran(Passed, 2*time.Hour)
	merge.Worktree, merge.WorktreeState, merge.Changes = &worktree.Worktree{Dir: "/w"}, WorktreeKept, worktree.Changes{Commits: 2}
	trig := Trigger{ID: "t1", Kind: TriggerCondition, Condition: &Condition{Command: "gh issue list", When: WhenChanged}}
	views := []View{
		{Automation: Automation{ID: "ok", Name: "Fine", Enabled: true}, Runs: []Run{ran(Passed, time.Hour)}},
		{Automation: Automation{ID: "fail", Name: "Nightly", Enabled: true}, Runs: []Run{ran(Passed, 5*time.Hour), ran(Failed, 3*time.Hour)}},
		{Automation: Automation{ID: "needs", Name: "Triage", Enabled: true}, Runs: []Run{needs}},
		{Automation: Automation{ID: "merge", Name: "Deps", Enabled: true}, Runs: []Run{merge}},
		{Automation: Automation{ID: "stall", Name: "Fix CI", Enabled: true}, Stalled: now.Add(-30 * time.Minute), Runs: []Run{ran(Passed, 30*time.Minute)}},
		{Automation: Automation{ID: "trig", Name: "Issues", Enabled: true, Triggers: []Trigger{trig}}, TriggerStates: map[string]TriggerState{"t1": {Errors: 3, Error: "gh: not logged in", LastProbe: now}}},
		// A failure of a run that is running again is not shown yet, nor
		// what a paused automation's triggers do.
		{Automation: Automation{ID: "busy", Name: "Busy", Enabled: true}, Running: true, Runs: []Run{ran(Failed, time.Hour), {Started: now, Result: Running}}},
		{Automation: Automation{ID: "paused", Name: "Paused", Triggers: []Trigger{trig}}, TriggerStates: map[string]TriggerState{"t1": {Errors: 5, Error: "x"}}},
		// A failed run followed by a passing one is over.
		{Automation: Automation{ID: "fixed", Name: "Fixed", Enabled: true}, Runs: []Run{ran(Failed, 3*time.Hour), ran(Passed, time.Hour)}},
	}
	got := NeedsAttention(views)
	want := []struct{ id, kind string }{
		{"stall", AttentionStalled}, {"needs", AttentionNeedsUser},
		{"merge", AttentionMerge}, {"fail", AttentionFailed},
		{"trig", AttentionTrigger},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Kind != w.kind || AttentionLabel(got[i].Kind) == "" {
			t.Fatalf("item %d: got %+v, want %s %s", i, got[i], w.id, w.kind)
		}
	}
	if got[4].Detail == "" || got[3].Detail != Failed {
		t.Fatalf("details: %+v", got)
	}
}
