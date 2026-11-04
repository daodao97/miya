package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func automationSnapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("MIYA_SNAPSHOT")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = png.Encode(f, tt.Image())
}

func TestAutomationsPage(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.cwd = t.TempDir()
	c.startAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	now := atLocal("2026-11-02 08:30")
	c.auto.now = func() time.Time { return now }
	issue, err := c.saveAutomation(Automation{Name: "每日 Issue 分拣", Project: c.cwd, Agent: agentClaude, Instructions: "查看最近 24 小时新开的 Issue。值得处理的直接修复并补测试，修不了的列出来说明原因。", Request: "每个工作日 9 点看新 Issue，能修就修", Schedule: "0 9 * * 1-5", Check: "go test ./...", Rounds: 3, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c.auto.store.addRun(issue.ID, AutomationRun{Started: atLocal("2026-11-01 09:00"), Finished: atLocal("2026-11-01 09:14"), Round: 2, Rounds: 3, Result: runPassed, Summary: "修复 #142 时区判断，补了测试", Thread: 99})
	c.auto.store.addRun(issue.ID, AutomationRun{Started: atLocal("2026-10-31 09:00"), Finished: atLocal("2026-10-31 09:31"), Round: 3, Rounds: 3, Result: runFailed, Summary: "检查未通过：FAIL demo/auth"})
	if _, err := c.saveAutomation(Automation{Name: "仓库日报", Project: c.cwd, Agent: agentCodex, Instructions: "写今天的仓库日报", Schedule: "0 18 * * *"}); err != nil {
		t.Fatal(err)
	}
	app.showAutomations()
	tt.Frame()
	tt.Frame()
	for _, want := range []string{"Automations", "每日 Issue 分拣", "Weekdays 9:00", "Failed · 3/3 turns", "仓库日报", "Paused", "New"} {
		if !tt.HasText(want) {
			t.Fatalf("list lacks %q: %q", want, tt.Texts())
		}
	}
	automationSnapshot(t, tt, "automations-list")

	app.autos.page, app.autos.detail = "detail", issue.ID
	tt.Frame()
	for _, want := range []string{"Run history", "Run now", "修复 #142 时区判断，补了测试", "go test ./...", "Last 2 runs"} {
		if !tt.HasText(want) {
			t.Fatalf("detail lacks %q: %q", want, tt.Texts())
		}
	}
	automationSnapshot(t, tt, "automations-detail")

	app.openAutomationForm(nil)
	app.autos.request = "每个工作日早上 9 点看一下新 Issue，能修的修掉并保证 go test 通过，修不过就多试两次"
	draft := automationViewOf(issue, nil, false, now)
	draft.ID = ""
	app.autos.draft = &draft
	tt.Frame()
	for _, want := range []string{"New automation", "Generated settings", "Ready", "Up to 3 turns", "0 9 * * 1-5", "Create"} {
		if !tt.HasText(want) {
			t.Fatalf("form lacks %q: %q", want, tt.Texts())
		}
	}
	automationSnapshot(t, tt, "automations-form")

	app.autos.draft.TimeMissing = true
	tt.Frame()
	if !tt.HasText("Schedule needed") {
		t.Fatalf("missing time not shown: %q", tt.Texts())
	}
	app.autos.draft, app.autos.drafting = nil, true
	tt.Frame()
	if !tt.HasText("Generating…") {
		t.Fatalf("drafting not shown: %q", tt.Texts())
	}
	automationSnapshot(t, tt, "automations-drafting")
}
