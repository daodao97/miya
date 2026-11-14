package integration

import (
	claude "miya/internal/agent/claude/native"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	phoneserver "miya/internal/phone/server"
	"testing"
)

func TestGoalEventsAndClaudeSchedulesStayOnTheirThread(t *testing.T) {
	c := testController()
	c.Cwd = "/a"
	c.ProjectCache = map[string][]model.Thread{"/b": {{ID: 3, ServerID: "background", Agent: appmodel.AgentCodex}}}
	c.Handle(event("thread/goal/updated", `{"threadId":"background","goal":{"objective":"fix CI","status":"active","tokenBudget":1000,"tokensUsed":120}}`))
	if len(c.Threads()[0].Tasks) != 0 || len(c.ProjectCache["/b"][0].Tasks) != 1 {
		t.Fatal("goal routed to the foreground thread")
	}
	c.Handle(event("thread/goal/updated", `{"threadId":"background","goal":{"objective":"fix CI","status":"budgetLimited","tokenBudget":1000,"tokensUsed":1000}}`))
	if got := appmodel.TaskStatusLabel(c.ProjectCache["/b"][0], c.ProjectCache["/b"][0].Tasks[0], false); got != "Budget exhausted" {
		t.Fatal(got)
	}
	c.Handle(event("thread/goal/cleared", `{"threadId":"background"}`))
	if len(c.ProjectCache["/b"][0].Tasks) != 0 {
		t.Fatal("goal was not cleared")
	}
	c.Update(2, func(t *model.Thread) { t.Agent = appmodel.AgentClaude })
	c.HandleClaude(2, claude.Frame{Type: "result", KeepingAlive: true, SchedulesKnown: true, Schedules: []claude.ScheduledTask{{ID: "job", Prompt: "check CI", Status: "waiting"}}})
	if c.Threads()[1].Busy || c.Threads()[1].Tasks[0].Status != "waiting" {
		t.Fatal("turn completion lost waiting schedule")
	}
	// Thread tasks travel with metadata, even when no message changed.
	sync := phoneserver.NewThreadSync(nil)
	sync.Update(c.Threads()[1])
	c.Update(2, func(t *model.Thread) { t.Tasks[0].Status = "completed" })
	delta, changed := sync.Update(c.Threads()[1])
	if !changed || len(delta.Changed) != 0 || delta.Thread.Tasks[0].Status != "completed" {
		t.Fatal("phone did not get task-only change")
	}
}

func TestGoalCommandParsing(t *testing.T) {
	g, ok, err := codex.ParseGoalCommand("/goal --budget 50000 fix /tmp/example")
	if !ok || err != nil || g.Budget == nil || *g.Budget != 50000 || g.Objective != "fix /tmp/example" {
		t.Fatalf("%+v %v", g, err)
	}
	_, _, err = codex.ParseGoalCommand("/goal --budget -1 fix")
	if err == nil {
		t.Fatal("negative budget accepted")
	}
	for _, a := range []string{"pause", "resume", "clear"} {
		g, _, _ := codex.ParseGoalCommand("/goal " + a)
		if g.Action != a {
			t.Fatal(g)
		}
	}
}

func TestFinishedClaudeLoopCanBeCleared(t *testing.T) {
	c := testController()
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, Loaded: true, HistoryLoaded: true}})
	schedules := func(status string) {
		c.HandleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, SchedulesKnown: true, Schedules: []claude.ScheduledTask{{ID: "wakeup", Prompt: "check CI", Status: status}}})
	}
	tasks := func() []model.ThreadTask { return c.ThreadWithID(1).Tasks }
	schedules("waiting")
	if msg := c.TaskAction(1, "loop:wakeup", "clear"); msg == "" || len(tasks()) != 1 {
		t.Fatalf("running loop was cleared: %q %v", msg, tasks())
	}
	schedules("completed")
	if len(tasks()) != 1 || tasks()[0].Status != "completed" {
		t.Fatalf("loop did not finish: %v", tasks())
	}
	if msg := c.TaskAction(1, "loop:wakeup", "clear"); msg != "" || len(tasks()) != 0 {
		t.Fatalf("finished loop not cleared: %q %v", msg, tasks())
	}
	// The same Claude session keeps reporting the ended schedule.
	schedules("completed")
	if len(tasks()) != 0 {
		t.Fatalf("cleared loop came back: %v", tasks())
	}
	schedules("waiting")
	if len(tasks()) != 1 {
		t.Fatal("new wakeup was dropped")
	}
}
