package automation

import (
	"strings"
	"testing"
	"time"
)

func TestCoverageIsKeptAndPartialRunsNotify(t *testing.T) {
	r := Report{Summary: "checked", Coverage: &Coverage{Expected: 48, Included: 60, Missing: []string{" ", "repo-a: 404"}}}.Cleaned()
	if r.Coverage == nil || r.Coverage.Included != 48 || len(r.Coverage.Missing) != 1 || r.Coverage.Partial() {
		t.Fatalf("cleaned: %+v", r.Coverage)
	}
	if r := (Report{Summary: "x", Coverage: &Coverage{}}).Cleaned(); r.Coverage != nil {
		t.Fatal("an empty coverage was kept")
	}
	partial := &Report{Summary: "checked", Findings: []string{"x"}, Coverage: &Coverage{Expected: 48, Included: 1, Missing: []string{"47 outside the registry"}}}
	if quietRun(Automation{}, Run{Result: Passed, Report: partial, Unchanged: true}) {
		t.Fatal("a run that covered 1 of 48 was quiet")
	}
	full := &Report{Summary: "checked", Findings: []string{"x"}, Coverage: &Coverage{Expected: 48, Included: 48}}
	if sameReport(*partial, *full) {
		t.Fatal("a change of coverage counted as nothing new")
	}
	if !strings.Contains(FormatReport(*partial), "Coverage: 1/48; missing: 47 outside the registry") {
		t.Fatalf("format: %s", FormatReport(*partial))
	}
	if CoverageLabel(Run{Report: partial}) == "" || CoverageLabel(Run{Report: &Report{}}) != "" {
		t.Fatal("coverage label")
	}
	now := atLocal("2026-11-15 10:00")
	views := []View{{Automation: Automation{ID: "a", Name: "Radar", Enabled: true}, Runs: []Run{{Started: now.Add(-time.Hour), Finished: now, Result: Passed, Report: partial}}}}
	if got := NeedsAttentionAt(views, now); len(got) != 1 || got[0].Kind != AttentionPartial || !strings.Contains(got[0].Detail, "1/48") {
		t.Fatalf("attention: %+v", got)
	}
}

func TestQuietLineCommandIsListed(t *testing.T) {
	now := atLocal("2026-11-15 10:00")
	line := Trigger{ID: "t", Kind: TriggerCondition, Condition: &Condition{Command: "tail -n 0 -F app.log", When: WhenLine, SilentHours: 6}}
	view := func(st TriggerState) View {
		return View{Automation: Automation{ID: "a", Name: "Errors", Enabled: true, Triggers: []Trigger{line}}, TriggerStates: map[string]TriggerState{"t": st}}
	}
	if got := NeedsAttentionAt([]View{view(TriggerState{LastSuccess: now.Add(-2 * time.Hour)})}, now); len(got) != 0 {
		t.Fatalf("listed within its hours: %+v", got)
	}
	got := NeedsAttentionAt([]View{view(TriggerState{LastSuccess: now.Add(-7 * time.Hour)})}, now)
	if len(got) != 1 || got[0].Kind != AttentionSilent {
		t.Fatalf("not listed after its hours: %+v", got)
	}
	// One that never printed counts from when it started.
	if got := NeedsAttentionAt([]View{view(TriggerState{LastProbe: now.Add(-8 * time.Hour), Met: true})}, now); len(got) != 1 {
		t.Fatalf("a command that never printed: %+v", got)
	}
	c := Condition{Command: "gh issue list", When: WhenChanged, SilentHours: 6}
	if err := c.normalize(); err != nil || c.SilentHours != 0 {
		t.Fatalf("silentHours kept on a changed command: %+v %v", c, err)
	}
}
