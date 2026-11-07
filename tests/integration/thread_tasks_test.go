package integration

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	claude "miya/internal/agent/claude/native"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	phoneserver "miya/internal/phone/server"
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

func TestTaskBarDesktopAndMobile(t *testing.T) {
	app, tt := testApp(t)
	budget := int64(50000)
	thread := model.Thread{ID: 1, ServerID: "s1", Title: "Tasks", HistoryLoaded: true, Tasks: []model.ThreadTask{{ID: "goal", Kind: "goal", Title: "修复 CI 并验证测试通过", Status: "paused", TokensUsed: 1200, TokenBudget: &budget}}}
	app.C.(*controller.Controller).SetThreads([]model.Thread{thread})
	app.C.(*controller.Controller).SetActive(1)
	tt.Frame()
	if !tt.HasText("Goal · Paused") || !tt.HasText("1200 / 50000 tokens") {
		t.Fatalf("task bar missing: %v", tt.Texts())
	}
	bar, ok := tt.Find("Goal · Paused")
	if !ok {
		t.Fatal("missing bar")
	}
	input, ok := tt.Find("Message")
	if ok && bar.Y >= input.Y {
		t.Fatal("task status is below input")
	}
	writeTaskSnapshot(t, "desktop-goal", tt)
	m := testMobileApp()
	m.Open = 1
	thread.Agent = appmodel.AgentClaude
	thread.Tasks = []model.ThreadTask{{ID: "loop:job", Kind: "loop", Title: "检查部署状态", Status: "waiting", Schedule: "*/5 * * * *", NextRunAtMs: time.Now().Add(5 * time.Minute).UnixMilli()}}
	m.ShowThread(thread)
	m.Router().Push("/thread")
	mt := ui.NewTester(m.ViewRoot, 390, 750)
	mt.Frame()
	if !mt.HasText("Loop · Waiting to run") {
		t.Fatalf("mobile task bar missing: %v", mt.Texts())
	}
	row, ok := mt.Find("Stop loop")
	if !ok || row.X < 0 || row.X+row.W > 390 {
		t.Fatalf("mobile action clipped: %+v", row)
	}
	writeTaskSnapshot(t, "mobile-loop", mt)
}

func writeTaskSnapshot(t *testing.T, name string, tt *ui.Tester) {
	t.Helper()
	if dir := os.Getenv("MIYA_TASK_SNAPSHOT"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
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

func TestFinishedLoopShowsClearAction(t *testing.T) {
	m := testMobileApp()
	m.Open = 1
	m.ShowThread(model.Thread{ID: 1, ServerID: "s1", Agent: appmodel.AgentClaude, Title: "Tasks", HistoryLoaded: true,
		Tasks: []model.ThreadTask{{ID: "loop:wakeup", Kind: "loop", Title: "检查部署状态", Status: "completed"}}})
	m.Router().Push("/thread")
	mt := ui.NewTester(m.ViewRoot, 390, 750)
	mt.Frame()
	if !mt.HasText("Loop · Completed") {
		t.Fatalf("task bar missing: %v", mt.Texts())
	}
	if _, ok := mt.Find("Clear loop"); !ok {
		t.Fatalf("clear action missing: %v", mt.Texts())
	}
	if _, ok := mt.Find("Stop loop"); ok {
		t.Fatal("finished loop still offers stop")
	}
}

func TestRunningSubAgentsStayAboveComposer(t *testing.T) {
	thread := model.Thread{ID: 1, ServerID: "s1", Title: "Agents", HistoryLoaded: true, Busy: true, Messages: []model.Message{
		{ID: 1, Role: "tool", ItemID: "call-a", Agents: []model.SubAgent{{ID: "a", Name: "Review the parser", Status: "running", Steps: 3, Detail: "Reading lexer.go"}}},
		{ID: 2, Role: "tool", ItemID: "call-b", Agents: []model.SubAgent{{ID: "b", Name: "Write the changelog", Status: "completed"}}},
		// Codex reports an agent again on a later call; the latest state wins.
		{ID: 3, Role: "tool", ItemID: "spawn", Agents: []model.SubAgent{{ID: "c", Name: "Agent 019a7f", Status: "running"}}},
		{ID: 4, Role: "tool", ItemID: "wait", Agents: []model.SubAgent{{ID: "c", Name: "Agent 019a7f", Status: "completed"}}},
	}}
	for _, agent := range []string{appmodel.AgentClaude, appmodel.AgentCodex} {
		thread.Agent = agent
		m := testMobileApp()
		m.Open = 1
		m.ShowThread(thread)
		m.Router().Push("/thread")
		mt := ui.NewTester(m.ViewRoot, 390, 750)
		mt.Frame()
		if !mt.HasText("Subagent · Running") || !mt.HasText("Review the parser") || !mt.HasText("3 steps · Reading lexer.go") {
			t.Fatalf("%s: running sub-agent missing above composer: %v", agent, mt.Texts())
		}
		if mt.HasText("Subagent · Completed") || strings.Count(strings.Join(mt.Texts(), "\n"), "Subagent · ") != 1 {
			t.Fatalf("%s: finished sub-agents listed: %v", agent, mt.Texts())
		}
	}
	app, tt := testApp(t)
	thread.Agent = appmodel.AgentClaude
	ctl := app.C.(*controller.Controller)
	ctl.SetThreads([]model.Thread{thread})
	ctl.SetActive(1)
	tt.Frame()
	row, ok := tt.Find("Review the parser")
	input, hasInput := tt.Find("Message")
	if !ok || hasInput && row.Y >= input.Y {
		t.Fatalf("desktop sub-agent row missing or below input: %v", tt.Texts())
	}
	ctl.Update(1, func(t *model.Thread) { t.Messages[0].Agents[0].Status = "completed" })
	tt.Frame()
	if tt.HasText("Subagent · Running") {
		t.Fatal("finished sub-agent stayed above composer")
	}
	ctl.Update(1, func(t *model.Thread) { t.Messages[0].Agents[0].Status = "running"; t.Busy = false })
	tt.Frame()
	if tt.HasText("Subagent · Running") {
		t.Fatal("stale sub-agent shown after the conversation stopped working")
	}
}
