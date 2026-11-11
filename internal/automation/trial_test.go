package automation

import (
	"strings"
	"testing"
)

func TestTryConditions(t *testing.T) {
	command := func(id, when, command string) Trigger {
		return Trigger{ID: id, Kind: TriggerCondition, Condition: &Condition{Command: command, When: when}}
	}
	off := command("off", WhenSuccess, "exit 7")
	off.Disabled = true
	a := Automation{Project: t.TempDir(), Triggers: []Trigger{
		command("met", WhenSuccess, "echo 3 jobs"),
		command("unmet", WhenSuccess, "echo idle; exit 1"),
		command("changed", WhenChanged, "echo v1"),
		command("line", WhenLine, "echo ready; sleep 30"),
		off,
		{ID: "file", Kind: TriggerFile, Path: "/tmp"},
	}}
	a.Policy.Gate = &Condition{Command: "true", When: WhenSuccess}
	if !a.HasConditions() {
		t.Fatal("an automation with conditions reports none")
	}
	trials, err := TryConditions(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(trials) != 5 {
		t.Fatalf("trials of a trigger that is off or has no command: %+v", trials)
	}
	if got := trials["met"].Result(); got != "met right now: 3 jobs" {
		t.Fatalf("met: %q", got)
	}
	// Not being met right now is a result, not a failure.
	if got := trials["unmet"].Result(); got != "not met right now: idle" {
		t.Fatalf("unmet: %q", got)
	}
	if got := trials["changed"].Result(); !strings.HasPrefix(got, "the command runs") {
		t.Fatalf("changed: %q", got)
	}
	if got := trials["line"].Result(); !strings.HasPrefix(got, "the command stays running") {
		t.Fatalf("line: %q", got)
	}
	if got := trials[GateTrial]; !got.Met || got.Text == "" {
		t.Fatalf("gate: %+v", got)
	}

	// A command that cannot be used fails the whole test run and is named.
	for _, bad := range []Trigger{
		command("bad", WhenSuccess, "echo no such queue >&2; exit 7"),
		command("bad", WhenChanged, "exit 1"),
		command("bad", WhenLine, "exit 0"),
	} {
		b := Automation{Project: a.Project, Triggers: []Trigger{command("met", WhenSuccess, "true"), bad}}
		trials, err := TryConditions(b)
		if err == nil || trials != nil || !strings.Contains(err.Error(), "The test run of the trigger") || !strings.Contains(err.Error(), "failed: ") {
			t.Fatalf("%s %q: %v, %+v", bad.Condition.When, bad.Condition.Command, err, trials)
		}
	}
	gate := Automation{Project: a.Project}
	gate.Policy.Gate = &Condition{Command: "exit 3", When: WhenSuccess}
	if _, err := TryConditions(gate); err == nil {
		t.Fatal("a gate whose command fails passed its test run")
	}

	// Nothing to run.
	none := Automation{Project: a.Project, Triggers: []Trigger{off, {ID: "file", Kind: TriggerFile, Path: "/tmp"}}}
	if none.HasConditions() {
		t.Fatal("a trigger that is off counts as a condition to run")
	}
	if trials, err := TryConditions(none); err != nil || trials != nil {
		t.Fatalf("nothing to try: %v, %+v", err, trials)
	}
}
