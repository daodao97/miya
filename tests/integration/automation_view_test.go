package integration

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/controller"
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
	c := app.C.(*controller.Controller)
	c.Cwd = t.TempDir()
	c.StartAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	now := atLocal("2026-11-02 08:30")
	c.Auto.Now = func() time.Time { return now }
	issue, err := c.Auto.Save(automation.Automation{Name: "每日 Issue 分拣", Project: c.Cwd, Agent: appmodel.AgentClaude, Instructions: "查看最近 24 小时新开的 Issue。值得处理的直接修复并补测试，修不了的列出来说明原因。", Request: "每个工作日 9 点看新 Issue，能修就修", Schedule: "0 9 * * 1-5", Check: "go test ./...", Rounds: 3, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Auto.Store.AddRun(issue.ID, automation.Run{Started: atLocal("2026-11-01 09:00"), Finished: atLocal("2026-11-01 09:14"), Round: 2, Rounds: 3, Result: automation.Passed, Summary: "修复 #142 时区判断，补了测试", Thread: 99})
	c.Auto.Store.AddRun(issue.ID, automation.Run{Started: atLocal("2026-10-31 09:00"), Finished: atLocal("2026-10-31 09:31"), Round: 3, Rounds: 3, Result: automation.Failed, Summary: "检查未通过：FAIL demo/auth"})
	if _, err := c.Auto.Save(automation.Automation{Name: "仓库日报", Project: c.Cwd, Agent: appmodel.AgentCodex, Instructions: "写今天的仓库日报", Schedule: "0 18 * * *"}); err != nil {
		t.Fatal(err)
	}
	app.ShowAutomations()
	tt.Frame()
	tt.Frame()
	for _, want := range []string{"Automations", "每日 Issue 分拣", "Weekdays 9:00", "Failed · 3/3 turns", "仓库日报", "Paused", "New"} {
		if !tt.HasText(want) {
			t.Fatalf("list lacks %q: %q", want, tt.Texts())
		}
	}
	automationSnapshot(t, tt, "automations-list")

	app.Autos.Page, app.Autos.Detail = "detail", issue.ID
	tt.Frame()
	for _, want := range []string{"Run history", "Run now", "修复 #142 时区判断，补了测试", "go test ./...", "Last 2 runs"} {
		if !tt.HasText(want) {
			t.Fatalf("detail lacks %q: %q", want, tt.Texts())
		}
	}
	automationSnapshot(t, tt, "automations-detail")

	app.OpenAutomationForm(nil)
	app.Autos.Request = "每个工作日早上 9 点看一下新 Issue，能修的修掉并保证 go test 通过，修不过就多试两次"
	draft := automation.ViewOf(issue, nil, false, now)
	draft.ID = ""
	app.Autos.Draft = &draft
	tt.Frame()
	for _, want := range []string{"New automation", "Generated settings", "Ready", "Up to 3 turns", "0 9 * * 1-5", "Create"} {
		if !tt.HasText(want) {
			t.Fatalf("form lacks %q: %q", want, tt.Texts())
		}
	}
	automationSnapshot(t, tt, "automations-form")

	app.Autos.Draft.TimeMissing = true
	tt.Frame()
	if !tt.HasText("Schedule needed") {
		t.Fatalf("missing time not shown: %q", tt.Texts())
	}
	app.Autos.Draft, app.Autos.Drafting = nil, true
	tt.Frame()
	if !tt.HasText("Generating…") {
		t.Fatalf("drafting not shown: %q", tt.Texts())
	}
	automationSnapshot(t, tt, "automations-drafting")
}

func TestAutomationDraftRequiresButton(t *testing.T) {
	app, tt := testApp(t)
	app.ShowAutomations()
	app.OpenAutomationForm(nil)
	tt.Frame()
	if err := tt.Click("Generate settings"); err != nil {
		t.Fatal(err)
	}
	if app.Autos.DraftErr != "" || app.Autos.Drafting {
		t.Fatal("empty input started generation")
	}
	if err := tt.Click("What to do"); err != nil {
		t.Fatal(err)
	}
	tt.Type("每天 14 点清理存储空间")
	tt.Frame()
	if app.Autos.Request == "" {
		t.Fatal("request input was not updated")
	}
	// Wait past the former debounce interval to catch accidental auto-generation.
	time.Sleep(1100 * time.Millisecond)
	tt.Frame()
	if app.Autos.Drafting || app.Autos.Draft != nil || app.Autos.DraftErr != "" {
		t.Fatal("typing started generation")
	}
	if err := tt.Click("Claude Code"); err != nil {
		t.Fatal(err)
	}
	if app.Autos.Agent != appmodel.AgentClaude || app.Autos.Drafting || app.Autos.DraftErr != "" {
		t.Fatal("agent selection started generation")
	}
	if err := tt.Click("Generate settings"); err != nil {
		t.Fatal(err)
	}
	// This controller has no service. Its error proves the button sent a request
	// without launching a real agent in the test.
	if app.Autos.DraftErr != "Automations require the Miya background service" {
		t.Fatalf("button did not request generation: %q", app.Autos.DraftErr)
	}
}

func TestAutomationInputDiscardsOldDraft(t *testing.T) {
	app, tt := testApp(t)
	app.ShowAutomations()
	app.OpenAutomationForm(nil)
	app.Autos.Request = "每天 9 点运行测试"
	app.Autos.Draft = &automation.View{Automation: automation.Automation{Name: "Old settings"}}
	tt.Frame()
	if err := tt.Click("What to do"); err != nil {
		t.Fatal(err)
	}
	tt.Type("，报告失败原因")
	tt.Frame()
	if app.Autos.Draft != nil || app.Autos.Drafting || tt.HasText("Ready") {
		t.Fatal("edited request kept old settings ready to save")
	}
	// With no settings yet, the form says which triggers a request can ask for.
	if !tt.HasText("Triggers you can ask for") || !tt.HasText("disk_free") || !tt.HasText("memory_free") {
		t.Fatal("the form does not list the ready-made triggers")
	}
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	if app.Autos.Saving || app.Autos.DraftErr != "" {
		t.Fatal("edited request allowed saving old settings")
	}
}

func TestSharedWebhookVisibleWithoutAutomations(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.StartAutomations(filepath.Join(t.TempDir(), "automations.json"), false)
	app.ShowAutomations()
	tt.Frame()
	tt.Frame()
	// The address belongs to Settings; Automations only links to it.
	if !tt.HasText("No automations yet") || tt.HasText("Copy webhook URL") {
		t.Fatalf("automations page should link to the webhook, not show it: %q", tt.Texts())
	}
	if err := tt.Click("Webhook"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if app.Section != "Settings" {
		t.Fatalf("webhook link opened %q", app.Section)
	}
	url := "https://tailcat-webhook.daodao973597.workers.dev/push/tcw_" + strings.Repeat("b", 64)
	app.Webhook.View = &automation.WebhookView{Enabled: true, URL: url, Messages: []automation.WebhookMessage{{ID: "msg", At: time.Now(), Data: "deploy finished", Detail: "No matching automation"}}}
	tt.Frame()
	for _, text := range []string{"Miya webhook", url, "Copy webhook URL", "deploy finished", "No matching automation"} {
		if !tt.HasText(text) {
			t.Fatalf("shared inbox missing %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Copy webhook URL"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != url {
		t.Fatal("global webhook address was not copied")
	}
	automationSnapshot(t, tt, "desktop-shared-webhook")
}
