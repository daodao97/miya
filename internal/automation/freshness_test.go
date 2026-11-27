package automation

import (
	"testing"
	"time"

	"miya/internal/chat/model"
)

// A command that succeeds without printing anything found nothing out when
// the condition says so; by default it is still an answer.
func TestEmptyOutputCountsAsFailedCheck(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		c       Condition
		err     bool
		met     bool
		failing string
	}{
		{name: "changed, empty, default", c: Condition{Command: "true", When: WhenChanged}},
		{name: "changed, empty, counted", c: Condition{Command: "true", When: WhenChanged, EmptyIsError: true}, err: true},
		{name: "changed, printed, counted", c: Condition{Command: "echo v1", When: WhenChanged, EmptyIsError: true}},
		{name: "success, empty, default", c: Condition{Command: "true", When: WhenSuccess}, met: true},
		{name: "success, empty, counted", c: Condition{Command: "true", When: WhenSuccess, EmptyIsError: true}, err: true},
		// Exit 1 answers "not now" with or without output.
		{name: "success, not met, counted", c: Condition{Command: "exit 1", When: WhenSuccess, EmptyIsError: true}},
	}
	for _, tc := range cases {
		r := runProbe(tc.c, dir)
		if (r.Err != "") != tc.err || (!tc.err && r.Met != tc.met) {
			t.Fatalf("%s: %+v", tc.name, r)
		}
		if tc.err && r.Err != emptyOutputError {
			t.Fatalf("%s: error %q", tc.name, r.Err)
		}
	}
	// The test run before saving goes the same way, so such a condition is
	// not saved while its source answers nothing.
	if r := TryCondition(Condition{Command: "true", When: WhenChanged, EmptyIsError: true}, dir); r.Err != emptyOutputError {
		t.Fatalf("trial: %+v", r)
	}
	// A command that keeps running is judged by the lines it prints.
	line := Condition{Command: "tail -n 0 -F app.log", When: WhenLine, EmptyIsError: true}
	if err := line.normalize(); err != nil || line.EmptyIsError {
		t.Fatalf("line condition: %+v %v", line, err)
	}
}

// A probe that keeps failing moves LastProbe on but leaves LastSuccess where
// the last answer was, so the pages can say how old it is.
func TestFailingProbeKeepsLastSuccess(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(Automation{Name: "Clean disk", Triggers: diskTrigger()})
	key := TriggerKey(a.ID, a.Triggers[0].ID)
	w.disk(10)
	w.wait(5 * time.Minute)
	answered := w.now
	if st := w.rn.Store.Triggers[key]; !st.LastSuccess.Equal(answered) || !st.LastProbe.Equal(answered) {
		t.Fatalf("after an answer: %+v", st)
	}
	w.probe = ProbeResult{Err: "no such volume"}
	w.wait(5 * time.Minute)
	w.wait(5 * time.Minute)
	st := w.rn.Store.Triggers[key]
	if !st.LastProbe.Equal(w.now) || !st.LastSuccess.Equal(answered) || st.Errors != 2 {
		t.Fatalf("after failing checks: %+v", st)
	}
	// It survives a restart with the rest of the trigger's state.
	w.rn.Persist()
	if st := w.runner().Store.Triggers[key]; !st.LastSuccess.Equal(answered) {
		t.Fatalf("after a restart: %+v", st)
	}
	w.disk(10)
	w.wait(5 * time.Minute)
	if st := w.rn.Store.Triggers[key]; !st.LastSuccess.Equal(w.now) || st.Errors != 0 {
		t.Fatalf("after it answers again: %+v", st)
	}
}

// A sentence that becomes a changed condition counts an empty answer as a
// failed check; one that becomes a met-or-not gate does not.
func TestDraftCountsEmptyChangesAsFailures(t *testing.T) {
	a, _, err := FromDraft(Draft{Name: "Release notes", Instructions: "write notes", TimeMissing: true, TriggerKind: draftCommand, TriggerWhen: WhenChanged, TriggerCommand: "git ls-remote --tags origin"},
		model.AgentClaude, "/ws", "有新 tag 时写发布说明")
	if err != nil || len(a.Triggers) != 1 || !a.Triggers[0].Condition.EmptyIsError {
		t.Fatalf("changed draft: %+v %v", a, err)
	}
	a, _, err = FromDraft(Draft{Name: "Drain", Instructions: "drain", Schedule: "0 9 * * *", TriggerKind: draftCommand, TriggerCommand: "test $(queue-depth) -gt 100"},
		model.AgentClaude, "/ws", "每天 9 点，队列超过 100 就处理")
	if err != nil || a.Policy.Gate == nil || a.Policy.Gate.EmptyIsError {
		t.Fatalf("gated draft: %+v %v", a, err)
	}
}
