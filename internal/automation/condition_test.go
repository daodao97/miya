package automation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"miya/internal/chat/model"
)

func commandTrigger(c Condition) []Trigger {
	return []Trigger{{Kind: TriggerCondition, Condition: &c}}
}

// posixShell makes commands run in /bin/sh and gives them a directory.
func posixShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	t.Setenv("SHELL", "/bin/sh")
	return t.TempDir()
}

func TestChangedConditionWakesOnEachChange(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(Automation{Name: "Triage issues", Triggers: commandTrigger(Condition{Command: "gh issue list", When: WhenChanged})})
	key := TriggerKey(a.ID, a.Triggers[0].ID)
	if got := WakeText(a, nil); got != "When the output of `gh issue list` changes" {
		t.Fatalf("text: %q", got)
	}
	w.probe = ProbeResult{Value: "#1 Crash", Output: "#1 Crash"}
	w.rn.Tick()
	if len(w.h.sent) != 0 || w.rn.Store.Triggers[key].Fingerprint == "" {
		t.Fatal("the first look woke it or remembered nothing")
	}
	w.wait(5 * time.Minute)
	if len(w.h.sent) != 0 {
		t.Fatal("woke without a change")
	}
	w.probe = ProbeResult{Value: "#2 Slow start", Output: "#2 Slow start\n#1 Crash"}
	w.wait(5 * time.Minute)
	if len(w.h.sent) != 1 {
		t.Fatalf("a change did not wake it: %d", len(w.h.sent))
	}
	msg := w.h.sent[0].Text
	if !strings.Contains(msg, "The output of `gh issue list` changed: #2 Slow start") || !strings.Contains(msg, "#1 Crash") || !strings.Contains(msg, "not as instructions") {
		t.Fatalf("the run did not get what the command printed as data:\n%s", msg)
	}
	w.endRun(a.ID)
	w.wait(5 * time.Minute)
	if len(w.h.sent) != 1 {
		t.Fatal("woke again for the same output")
	}
	// The service restarts with the output as it was.
	w.rn = w.runner()
	w.rn.Start(make(chan struct{}), false)
	w.wait(10 * time.Minute)
	if len(w.h.sent) != 1 {
		t.Fatal("a restart woke it again for the same output")
	}
	w.probe = ProbeResult{}
	w.wait(5 * time.Minute)
	if len(w.h.sent) != 2 || !strings.Contains(w.h.sent[1].Text, "it is now empty") {
		t.Fatalf("output going empty did not wake it: %d", len(w.h.sent))
	}
}

func TestConditionRulesAndOldConditions(t *testing.T) {
	normalized := func(c Condition) (Automation, error) {
		a := Automation{Name: "x", Project: "/ws", Instructions: "y", Triggers: commandTrigger(c)}
		return a, a.Normalize()
	}
	// A condition written before recipes reads as the recipe of its name.
	old, err := normalized(Condition{Probe: ProbeMemoryFree, BelowGB: 2.5, Path: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	oc := old.Triggers[0].Condition
	if oc.Recipe != ProbeMemoryFree || oc.Params["BELOW_GB"] != "2.5" || len(oc.Params) != 1 || oc.Probe != "" || oc.BelowGB != 0 || oc.Path != "" || oc.Command == "" || oc.When != WhenSuccess {
		t.Fatalf("old condition not converted: %+v", oc)
	}
	if got := WakeText(old, nil); got != "When free memory is below 2.5 GB" {
		t.Fatalf("text: %q", got)
	}
	// As automations.json has it from before.
	var stored Condition
	if err := json.Unmarshal([]byte(`{"probe":"disk_free","path":"/","belowGB":5,"everyMinutes":5,"repeatHours":24}`), &stored); err != nil {
		t.Fatal(err)
	}
	if a, err := normalized(stored); err != nil || WakeText(a, nil) != "When free space on / is below 5 GB" || a.Triggers[0].Condition.Params["VOLUME"] != "/" {
		t.Fatalf("stored condition: %+v %v", a.Triggers, err)
	}
	// A probe of a stored condition that was not saved again still runs its recipe.
	if r := runProbe(Condition{Probe: "no_such", BelowGB: 5}, t.TempDir()); r.Err == "" {
		t.Fatalf("an old condition without a recipe was probed: %+v", r)
	}
	// An agent may give an amount as a number.
	var loose Condition
	if err := json.Unmarshal([]byte(`{"recipe":"disk_free","params":{"BELOW_GB":5,"VOLUME":"/data"}}`), &loose); err != nil {
		t.Fatal(err)
	}
	if a, err := normalized(loose); err != nil || WakeText(a, nil) != "When free space on /data is below 5 GB" {
		t.Fatalf("number parameter: %+v %v", a.Triggers, err)
	}
	// A recipe that is gone leaves a saved condition as it was.
	if a, err := normalized(Condition{Recipe: "gone", Command: "true"}); err != nil || a.Triggers[0].Condition.Command != "true" {
		t.Fatalf("saved condition of a missing recipe: %+v %v", a.Triggers, err)
	}
	line, err := normalized(Condition{Command: "tail -F app.log", When: WhenLine, EveryMinutes: 9})
	if err != nil || line.Triggers[0].Condition.EveryMinutes != 0 || WakeText(line, nil) != "When `tail -F app.log` prints a line" {
		t.Fatalf("line condition: %+v %v", line.Triggers, err)
	}
	for _, bad := range []Condition{{}, {Recipe: "no_such"}, {Recipe: ProbeDiskFree}, {Command: "x", When: "sometimes"}, {Probe: "cpu_load", BelowGB: 1}, {Probe: ProbeCommand}} {
		if _, err := normalized(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	for _, when := range []string{WhenChanged, WhenLine} {
		gated := Automation{Name: "x", Project: "/ws", Instructions: "y", Schedule: "0 9 * * *", Policy: WakePolicy{Gate: &Condition{Command: "x", When: when}}}
		if gated.Normalize() == nil {
			t.Fatalf("a gate that waits for %s was accepted", when)
		}
	}
}

func TestRecipeFromTheUsersDirectory(t *testing.T) {
	dir := posixShell(t)
	t.Setenv("MIYA_ISOLATED_DIR", dir)
	if err := os.MkdirAll(RecipeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := `{"description": "the job queue is longer than a limit", "command": "test \"$(cat \"$QUEUE_FILE\")\" -gt \"$LIMIT\"", "params": [{"name": "QUEUE_FILE", "description": "file with the length of the queue"}, {"name": "LIMIT", "default": "100"}], "text": "When the queue is longer than $LIMIT"}`
	for name, content := range map[string]string{"queue_depth.json": recipe, "broken.json": "{", "notes.txt": "not a recipe"} {
		if err := os.WriteFile(filepath.Join(RecipeDir(), name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list, problems := Recipes()
	r, ok := FindRecipe("queue_depth")
	if !ok || r.Bundled || len(list) != 3 || len(problems) != 1 || !strings.HasPrefix(problems[0], "broken.json: ") {
		t.Fatalf("recipes: %+v, problems %v", list, problems)
	}
	if b, ok := FindRecipe(ProbeDiskFree); !ok || !b.Bundled {
		t.Fatalf("bundled recipe: %+v", b)
	}
	hints := RecipeHints()
	if !strings.Contains(hints, "queue_depth: the job queue is longer than a limit [params: QUEUE_FILE = file with the length of the queue, LIMIT (default 100)]") || !strings.Contains(automationDraftPrompt("x", dir, time.Now()), "queue_depth") {
		t.Fatalf("the recipe is not offered: %s", hints)
	}
	queue := filepath.Join(dir, "queue")
	a := Automation{Name: "Drain", Project: dir, Instructions: "drain", Triggers: commandTrigger(Condition{Recipe: "queue_depth", Params: Params{"QUEUE_FILE": queue}})}
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	c := *a.Triggers[0].Condition
	if c.Params["LIMIT"] != "100" || WakeText(a, nil) != "When the queue is longer than 100" {
		t.Fatalf("condition: %+v", c)
	}
	if r := runProbe(c, dir); r.Err == "" {
		t.Fatalf("a command that could not look counted as an answer: %+v", r)
	}
	if err := os.WriteFile(queue, []byte("40\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := runProbe(c, dir); r.Err != "" || r.Met {
		t.Fatalf("short queue: %+v", r)
	}
	if err := os.WriteFile(queue, []byte("140\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := runProbe(c, dir); r.Err != "" || !r.Met {
		t.Fatalf("long queue: %+v", r)
	}
	if reason := conditionReason(c, ProbeResult{Met: true, Value: "140 jobs"}); reason != "When the queue is longer than 100: 140 jobs" {
		t.Fatalf("reason: %q", reason)
	}
	missing := Automation{Name: "x", Project: dir, Instructions: "y", Triggers: commandTrigger(Condition{Recipe: "queue_depth"})}
	if err := missing.Normalize(); err == nil || !strings.Contains(err.Error(), "QUEUE_FILE") {
		t.Fatalf("a recipe without its parameter: %v", err)
	}
}

func TestBundledRecipesRun(t *testing.T) {
	dir := posixShell(t)
	for recipe, found := range map[string]string{ProbeDiskFree: "GB free on /", ProbeMemoryFree: "GB of memory available"} {
		for limit, met := range map[string]bool{"0": false, "100000000": true} {
			c := Condition{Recipe: recipe, Params: Params{"BELOW_GB": limit}}
			if err := c.normalize(); err != nil {
				t.Fatal(err)
			}
			if r := runProbe(c, dir); r.Err != "" || r.Met != met || !strings.Contains(r.Value, found) {
				t.Errorf("%s below %s: %+v", recipe, limit, r)
			}
		}
	}
}

func TestCommandExitStatusAndOutput(t *testing.T) {
	dir := posixShell(t)
	probe := func(command, when string) ProbeResult {
		return runProbe(Condition{Command: command, When: when}, dir)
	}
	if r := probe("true", WhenSuccess); !r.Met || r.Err != "" {
		t.Fatalf("success: %+v", r)
	}
	if r := probe("echo low; exit 1", WhenSuccess); r.Met || r.Err != "" || r.Value != "low" {
		t.Fatalf("not now: %+v", r)
	}
	if r := probe("echo oops >&2; exit 3", WhenSuccess); r.Met || !strings.Contains(r.Err, "oops") {
		t.Fatalf("an error counted as an answer: %+v", r)
	}
	if r := probe(`printf 'a\nb\n'`, WhenChanged); r.Value != "a" || r.Output != "a\nb" || r.Err != "" || r.Met {
		t.Fatalf("output: %+v", r)
	}
	if r := probe("echo a; exit 1", WhenChanged); r.Err == "" {
		t.Fatalf("the output of a failed command counted: %+v", r)
	}
	if r := runProbe(Condition{Command: `test "$LIMIT" = 7`, Params: Params{"LIMIT": "7"}}, dir); !r.Met {
		t.Fatalf("parameters did not reach the command: %+v", r)
	}
	if r := probe("true", WhenLine); r.Err == "" {
		t.Fatalf("a command that keeps running was probed: %+v", r)
	}
	if r := TryCondition(Condition{Command: "exit 1"}, dir); r.Err != "" || r.Met {
		t.Fatalf("try: %+v", r)
	}
	if r := TryCondition(Condition{Command: "echo up; sleep 30", When: WhenLine}, dir); r.Err != "" || !r.Met || r.Value != "up" {
		t.Fatalf("try a line command: %+v", r)
	}
	if r := TryCondition(Condition{Command: "true", When: WhenLine}, dir); r.Err == "" {
		t.Fatalf("a line command that exits at once passed: %+v", r)
	}
}

func TestStreamRunsAndStopsCommand(t *testing.T) {
	dir := posixShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan StreamEvent, 16)
	done := make(chan error, 1)
	go func() {
		done <- streamOnce(ctx, dir, Condition{Command: "echo one; echo; echo two; sleep 30", When: WhenLine}, func(ev StreamEvent) { events <- ev })
	}()
	next := func() StreamEvent {
		t.Helper()
		select {
		case ev := <-events:
			return ev
		case <-time.After(10 * time.Second):
			t.Fatal("the command reported nothing")
		}
		return StreamEvent{}
	}
	if ev := next(); !ev.Started {
		t.Fatalf("first event: %+v", ev)
	}
	if one, two := next(), next(); one.Line != "one" || two.Line != "two" {
		t.Fatalf("lines: %+v %+v", one, two)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stopping left the command running")
	}
	err := streamOnce(context.Background(), dir, Condition{Command: "echo bye >&2; exit 3", When: WhenLine}, func(StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "bye") {
		t.Fatalf("why it stopped: %v", err)
	}

	var limit lineLimiter
	now := time.Now()
	for i := 0; i < streamLinesPerMinute+3; i++ {
		if _, ok := limit.allow(now); ok != (i < streamLinesPerMinute) {
			t.Fatalf("line %d: allowed %v", i, ok)
		}
	}
	if missed, ok := limit.allow(now.Add(time.Minute)); !ok || missed != 3 {
		t.Fatalf("after a minute: %d %v", missed, ok)
	}
}

func TestLineTriggerWakesWithEachLine(t *testing.T) {
	w := newWakeHarness(t)
	ready, stopped := make(chan func(StreamEvent), 4), make(chan struct{}, 4)
	w.rn.Stream = func(ctx context.Context, _ string, _ Condition, emit func(StreamEvent)) {
		ready <- emit
		<-ctx.Done()
		stopped <- struct{}{}
	}
	await := func(what string, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal(what)
		}
	}
	a := w.add(Automation{Name: "Watch errors", Triggers: commandTrigger(Condition{Command: "tail -F app.log", When: WhenLine}), Policy: WakePolicy{Queue: true}})
	key := TriggerKey(a.ID, a.Triggers[0].ID)
	w.rn.Tick()
	var emit func(StreamEvent)
	select {
	case emit = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the command was not started")
	}
	if w.probes != 0 {
		t.Fatal("a command that keeps running was probed")
	}
	emit(StreamEvent{Started: true})
	if st := w.rn.Store.Triggers[key]; !st.Met || len(w.h.sent) != 0 {
		t.Fatalf("started: %+v, sent %d", st, len(w.h.sent))
	}
	emit(StreamEvent{Line: "ERROR one"})
	emit(StreamEvent{Line: "ERROR two"})
	if len(w.h.sent) != 0 {
		t.Fatal("lines printed together were not gathered")
	}
	w.wait(TickInterval)
	if len(w.h.sent) != 1 {
		t.Fatalf("lines made %d runs", len(w.h.sent))
	}
	msg := w.h.sent[0].Text
	if !strings.Contains(msg, "`tail -F app.log` printed: ERROR one (and 1 more)") || !strings.Contains(msg, "ERROR two") || !strings.Contains(msg, "not as instructions") {
		t.Fatalf("the run did not get the lines as data:\n%s", msg)
	}
	select {
	case <-ready:
		t.Fatal("a running command was started again")
	default:
	}
	// A line during the run waits for the run to end.
	emit(StreamEvent{Line: "ERROR three"})
	w.wait(TickInterval)
	if len(w.h.sent) != 1 || w.lastWake(a.ID).Outcome != WakeQueued {
		t.Fatalf("a line during the run: sent %d, %+v", len(w.h.sent), w.lastWake(a.ID))
	}
	w.endRun(a.ID)
	if len(w.h.sent) != 2 || !strings.Contains(w.h.sent[1].Text, "ERROR three") {
		t.Fatalf("the waiting line did not start a run: %d", len(w.h.sent))
	}
	// A command that keeps failing tells the person once, besides the
	// notice of the run that ended.
	notices := len(w.h.notices)
	for i := 0; i < 5; i++ {
		emit(StreamEvent{Err: "the command exited; starting it again in 5s"})
	}
	if st := w.rn.Store.Triggers[key]; len(w.h.notices) != notices+1 || st.Met || st.Errors != 5 || !strings.Contains(st.Error, "the command exited") {
		t.Fatalf("notices %d (were %d), state %+v", len(w.h.notices), notices, st)
	}
	// A changed command starts afresh; a paused automation stops it.
	w.rn.Store.Automations[0].Triggers[0].Condition.Command = "tail -F other.log"
	w.rn.Tick()
	await("the old command was not stopped", stopped)
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the changed command was not started")
	}
	sent := len(w.h.sent)
	emit(StreamEvent{Line: "late"})
	w.wait(TickInterval)
	if len(w.h.sent) != sent {
		t.Fatal("a stopped command still woke the automation")
	}
	w.rn.Store.Automations[0].Enabled = false
	w.rn.Tick()
	await("pausing left the command running", stopped)
	if len(w.rn.wakes().streams) != 0 {
		t.Fatal("a stopped command is still listed")
	}
}

func TestDraftWithCommandsAndRecipes(t *testing.T) {
	a, missing, err := FromDraft(Draft{Name: "Release notes", Instructions: "write notes", TimeMissing: true, TriggerKind: draftCommand, TriggerWhen: WhenChanged, TriggerCommand: "git ls-remote --tags origin"},
		model.AgentClaude, "/ws", "有新 tag 时写发布说明")
	if err != nil || missing || a.Schedule != "" || len(a.Triggers) != 1 || a.Triggers[0].Condition.When != WhenChanged {
		t.Fatalf("changed draft: %+v %v %v", a, missing, err)
	}
	// A change is an event: with a time too, both wake it; it is no gate.
	a, _, err = FromDraft(Draft{Name: "Release notes", Instructions: "write notes", Schedule: "0 9 * * *", TriggerKind: draftCommand, TriggerWhen: WhenChanged, TriggerCommand: "git ls-remote --tags origin"},
		model.AgentClaude, "/ws", "每天 9 点，以及有新 tag 时")
	if err != nil || a.Schedule == "" || len(a.Triggers) != 1 || a.Policy.Gate != nil {
		t.Fatalf("changed draft with a time: %+v %v", a, err)
	}
	// A command that is met or not gates a time.
	a, _, err = FromDraft(Draft{Name: "Drain", Instructions: "drain", Schedule: "0 9 * * *", TriggerKind: draftCommand, TriggerCommand: "test $(queue-depth) -gt 100"},
		model.AgentClaude, "/ws", "每天 9 点，队列超过 100 就处理")
	if err != nil || len(a.Triggers) != 0 || a.Policy.Gate == nil || a.Policy.Gate.When != WhenSuccess {
		t.Fatalf("gated command draft: %+v %v", a, err)
	}
	// Lines are events that should not be lost during a run.
	a, missing, err = FromDraft(Draft{Name: "Watch errors", Instructions: "look", TimeMissing: true, TriggerKind: draftCommand, TriggerWhen: WhenLine, TriggerCommand: "tail -n 0 -F app.log"},
		model.AgentClaude, "/ws", "日志里出现 ERROR 时看一下")
	if err != nil || missing || a.Schedule != "" || len(a.Triggers) != 1 || a.Triggers[0].Condition.When != WhenLine || !a.Policy.Queue {
		t.Fatalf("line draft: %+v %v", a, err)
	}
	a, _, err = FromDraft(Draft{Name: "Free memory", Instructions: "report", TimeMissing: true, TriggerKind: draftRecipe, TriggerRecipe: ProbeMemoryFree, TriggerParams: []DraftParam{{Name: "BELOW_GB", Value: "8"}}},
		model.AgentClaude, "/ws", "可用内存低于 8G 时")
	if err != nil || len(a.Triggers) != 1 || WakeText(a, nil) != "When free memory is below 8 GB" {
		t.Fatalf("recipe draft: %+v %v", a, err)
	}
	if _, _, err := FromDraft(Draft{Name: "x", Instructions: "y", TimeMissing: true, TriggerKind: draftRecipe, TriggerRecipe: "no_such"}, model.AgentClaude, "/ws", "y"); err == nil {
		t.Fatal("a draft with a recipe that does not exist was accepted")
	}
	prompt := automationDraftPrompt("x", t.TempDir(), time.Now())
	for _, want := range []string{ProbeDiskFree + ": ", ProbeMemoryFree + ": ", "BELOW_GB"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the draft prompt does not offer %q", want)
		}
	}
	if !json.Valid([]byte(automationDraftSchema)) || !strings.Contains(automationDraftSchema, `"trigger_recipe"`) {
		t.Fatal("the draft schema does not ask for a recipe")
	}
}
