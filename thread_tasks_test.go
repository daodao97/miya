package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/claude"
)

func TestGoalEventsAndClaudeSchedulesStayOnTheirThread(t *testing.T) {
	c := testController()
	c.cwd = "/a"
	c.projectCache = map[string][]Thread{"/b": {{ID: 3, ServerID: "background", Agent: agentCodex}}}
	c.handle(event("thread/goal/updated", `{"threadId":"background","goal":{"objective":"fix CI","status":"active","tokenBudget":1000,"tokensUsed":120}}`))
	if len(c.threads()[0].Tasks) != 0 || len(c.projectCache["/b"][0].Tasks) != 1 {
		t.Fatal("goal routed to the foreground thread")
	}
	c.handle(event("thread/goal/updated", `{"threadId":"background","goal":{"objective":"fix CI","status":"budgetLimited","tokenBudget":1000,"tokensUsed":1000}}`))
	if got := taskStatusLabel(c.projectCache["/b"][0], c.projectCache["/b"][0].Tasks[0], false); got != "Budget exhausted" {
		t.Fatal(got)
	}
	c.handle(event("thread/goal/cleared", `{"threadId":"background"}`))
	if len(c.projectCache["/b"][0].Tasks) != 0 {
		t.Fatal("goal was not cleared")
	}
	c.update(2, func(t *Thread) { t.Agent = agentClaude })
	c.handleClaude(2, claude.Frame{Type: "result", KeepingAlive: true, SchedulesKnown: true, Schedules: []claude.ScheduledTask{{ID: "job", Prompt: "check CI", Status: "waiting"}}})
	if c.threads()[1].Busy || c.threads()[1].Tasks[0].Status != "waiting" {
		t.Fatal("turn completion lost waiting schedule")
	}
	// Thread tasks travel with metadata, even when no message changed.
	sync := newPhoneThreadSync(nil)
	sync.update(c.threads()[1])
	c.update(2, func(t *Thread) { t.Tasks[0].Status = "completed" })
	delta, changed := sync.update(c.threads()[1])
	if !changed || len(delta.Changed) != 0 || delta.Thread.Tasks[0].Status != "completed" {
		t.Fatal("phone did not get task-only change")
	}
}

func TestTaskBarDesktopAndMobile(t *testing.T) {
	app, tt := testApp(t)
	budget := int64(50000)
	thread := Thread{ID: 1, ServerID: "s1", Title: "Tasks", HistoryLoaded: true, Tasks: []ThreadTask{{ID: "goal", Kind: "goal", Title: "修复 CI 并验证测试通过", Status: "paused", TokensUsed: 1200, TokenBudget: &budget}}}
	app.c.setThreads([]Thread{thread})
	app.c.setActive(1)
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
	m.open = 1
	thread.Agent = agentClaude
	thread.Tasks = []ThreadTask{{ID: "loop:job", Kind: "loop", Title: "检查部署状态", Status: "waiting", Schedule: "*/5 * * * *", NextRunAtMs: time.Now().Add(5 * time.Minute).UnixMilli()}}
	m.showThread(thread)
	m.router().Push("/thread")
	mt := ui.NewTester(m.viewRoot, 390, 750)
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
	g, ok, err := parseGoalCommand("/goal --budget 50000 fix /tmp/example")
	if !ok || err != nil || g.budget == nil || *g.budget != 50000 || g.objective != "fix /tmp/example" {
		t.Fatalf("%+v %v", g, err)
	}
	_, _, err = parseGoalCommand("/goal --budget -1 fix")
	if err == nil {
		t.Fatal("negative budget accepted")
	}
	for _, a := range []string{"pause", "resume", "clear"} {
		g, _, _ := parseGoalCommand("/goal " + a)
		if g.action != a {
			t.Fatal(g)
		}
	}
}
