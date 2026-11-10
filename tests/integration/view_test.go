package integration

import (
	"context"
	"encoding/json"
	"miya/internal/agent/cliupdate"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent/account"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	desktopui "miya/internal/ui"
	"miya/internal/uiruntime"
)

// testApp builds the app around a controller that has no Codex process, so
// views run without a window, as fast as a unit test.
func testApp(t *testing.T) (*desktopui.App, *ui.Tester) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	uiruntime.InlineDispatch.Store(true)
	t.Cleanup(func() { uiruntime.InlineDispatch.Store(false) })
	cwd := t.TempDir()
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: cwd, ReferenceSource: testReferenceSource}}
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	t.Cleanup(c.Cancel)
	// Opening Settings checks the agent CLIs' versions: no test runs a
	// package manager, and no check returns before its test ends.
	idle := func(ctx context.Context, _, _ string, _ []string) cliupdate.Info {
		<-ctx.Done()
		return cliupdate.Info{}
	}
	c.CheckAgentCLI, c.UpgradeAgentCLI = idle, idle
	c.Agent, c.SetAgent = uiruntime.CreateSignal("codex")
	c.DraftPermission, c.SetDraftPermission = uiruntime.CreateSignal("")
	c.Projects, c.SetProjects = uiruntime.CreateSignal([]projectmodel.Project{{Name: "demo", Path: cwd}})
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Model, c.SetModel = uiruntime.CreateSignal("gpt-test")
	c.Effort, c.SetEffort = uiruntime.CreateSignal("medium")
	c.PermissionPolicy, c.SetPermissionPolicy = uiruntime.CreateSignal("on-request")
	c.Locale, c.SetLocale = uiruntime.CreateSignal("en")
	c.Models, c.SetModels = uiruntime.CreateSignal([]codex.Model{})
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.Status, c.SetStatus = uiruntime.CreateSignal("Connecting to Codex…")
	c.Ready, c.SetReady = uiruntime.CreateSignal(false)
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.HistoryStatus, c.SetHistoryStatus = uiruntime.CreateSignal("")
	c.Revision, c.SetRevision = uiruntime.CreateSignal(0)
	app := desktopui.NewChatAppWithController(c)
	app.ProjectName = "demo"
	return app, ui.NewTester(app.Render, 1180, 820)
}

func TestHomeShowsProjectAndSuggestions(t *testing.T) {
	app, tt := testApp(t)
	for _, want := range []string{"Let’s build", "demo", "Create a classic snake game", "New thread", "Projects"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Find and fix a bug in my code"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(app.C.(*controller.Controller).Draft(), "Find and fix a bug") {
		t.Fatalf("suggestion did not fill the draft: %q", app.C.(*controller.Controller).Draft())
	}
}

func TestConversationStreamsAndOffersCopyAfterTurn(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Title: "Greeting", Loaded: true, HistoryLoaded: true, Resumed: true}})
	c.SetActive(1)
	c.Update(1, func(t *model.Thread) {
		t.Messages = []model.Message{{ID: 10, Role: "user", Content: "Say **hi**"}}
	})
	c.Handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	c.Handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"a","delta":"Hello "}`))
	tt.Frame()
	if !tt.HasText("Greeting") || !tt.HasText("hi") {
		t.Fatalf("conversation not shown: %q", tt.Texts())
	}
	if _, ok := tt.Find("Stop response"); !ok {
		t.Fatal("busy conversation has no stop button")
	}
	c.Handle(event("item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"Hello world"}}`))
	c.Handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"completed"}}`))
	tt.Advance(2e9)
	tt.Frame()
	r, ok := tt.Find("Hello world")
	if !ok {
		t.Fatalf("final reply missing: %q", tt.Texts())
	}
	tt.Move(r.X+4, r.Y+4)
	tt.Frame()
	if err := tt.Click("Copy response"); err != nil {
		t.Fatalf("no copy action after the turn: %v", err)
	}
	if got := tt.Clipboard(); got != "Hello world" {
		t.Fatalf("copied %q", got)
	}
}

func TestActivitiesCollapseIntoOneRow(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Title: "Tools", Loaded: true, HistoryLoaded: true}})
	c.SetActive(1)
	c.Update(1, func(t *model.Thread) {
		t.Messages = []model.Message{
			{ID: 1, Role: "user", Content: "run"},
			{ID: 2, Role: "tool", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "ls -la"}, Content: "out"},
			{ID: 3, Role: "tool", Kind: "commandExecution", Status: "failed", Item: &model.Activity{Command: "go test"}, Content: "FAIL"},
			{ID: 4, Role: "assistant", Content: "Done"},
		}
	})
	tt.Frame()
	if !tt.HasText("2 activities") || tt.HasText("Ran ls -la") {
		t.Fatalf("activities not collapsed: %q", tt.Texts())
	}
	if err := tt.Click("2 activities"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Ran ls -la") || !tt.HasText("Failed · go test") {
		t.Fatalf("activities not expanded: %q", tt.Texts())
	}
	if err := tt.Click("Failed · go test"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("FAIL") {
		t.Fatalf("tool output not shown: %q", tt.Texts())
	}
}

func TestMessageListTextCanBeSelectedAndCopied(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "running"}[busy], func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Busy: busy, Loaded: true, HistoryLoaded: true, Messages: []model.Message{
				{ID: 1, Role: "user", Content: "Select this user message 鼠标拖选"},
				{ID: 2, Role: "assistant", Content: "Select this assistant reply 正文复制\n\n" + strings.Repeat("Longer message text for scrolling.\n\n", 45)},
			}}})
			c.SetActive(1)
			tt.Frame()
			app.ListFor(1).ScrollTo(0, ui.Start)
			tt.Frame()
			for _, text := range []string{"Select this user message 鼠标拖选", "Select this assistant reply 正文复制"} {
				r, ok := tt.Find(text)
				if !ok {
					t.Fatalf("message missing: %v", tt.Texts())
				}
				tt.Press(r.X+1, r.Y+r.H/2)
				// Native windows can repaint while a drag is in progress.
				tt.Frame()
				tt.Move(r.X+r.W-1, r.Y+r.H/2)
				tt.Frame()
				tt.Release(r.X+r.W-1, r.Y+r.H/2)
				tt.Key(ui.Cmd, ui.KeyC)
				if got := tt.Clipboard(); got != text {
					t.Fatalf("drag-selected message copied %q, want %q", got, text)
				}
			}
		})
	}
}

func TestMessageSelectionStaysPutWhileResponseGrows(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	const text = "Select this assistant reply 鼠标拖选正文"
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Busy: true, Loaded: true, HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "question"},
		{ID: 2, Role: "assistant", Content: text, Status: "completed"},
	}}})
	c.SetActive(1)
	tt.Frame()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatal("reply missing")
	}
	tt.Press(r.X+1, r.Y+r.H/2)
	c.Update(1, func(th *model.Thread) {
		th.Messages[1].Content += "\n\n" + strings.Repeat("More output arrives while selecting.\n\n", 40)
	})
	tt.Frame()
	tt.Advance(time.Second)
	tt.Move(r.X+r.W-1, r.Y+r.H/2)
	tt.Release(r.X+r.W-1, r.Y+r.H/2)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != text {
		t.Fatalf("new output disrupted selection: copied %q", got)
	}
}

func TestApprovalCardResolvesRequest(t *testing.T) {
	app, tt := testApp(t)
	id, _ := json.Marshal("claude:1:a1")
	app.C.(*controller.Controller).SetApprovals([]codex.Approval{{ID: id, Method: "claude/permission", Command: "Bash", Reason: `{"command":"pwd"}`}})
	tt.Frame()
	if !tt.HasText("Claude Code requests approval") {
		t.Fatalf("approval missing: %q", tt.Texts())
	}
	if err := tt.Click("Decline"); err != nil {
		t.Fatal(err)
	}
	if len(app.C.(*controller.Controller).Approvals()) != 0 || tt.HasText("Claude Code requests approval") {
		t.Fatal("declined approval still pending")
	}
}

func TestQuestionNeedsEveryAnswer(t *testing.T) {
	app, tt := testApp(t)
	id, _ := json.Marshal("claude:1:q1")
	app.C.(*controller.Controller).SetApprovals([]codex.Approval{{ID: id, Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q", Question: "Pick a color", Options: []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	}{{Label: "Blue", Description: "calm"}}}}}})
	tt.Frame()
	if err := tt.Click("Submit answers"); err != nil {
		t.Fatal(err)
	}
	if len(app.C.(*controller.Controller).Approvals()) != 1 {
		t.Fatal("submitted without an answer")
	}
	if err := tt.Click("Blue · calm"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Submit answers"); err != nil {
		t.Fatal(err)
	}
	if len(app.C.(*controller.Controller).Approvals()) != 0 {
		t.Fatal("answered question still pending")
	}
}

func TestSlashStatusShowsNotice(t *testing.T) {
	app, tt := testApp(t)
	tt.Type("/stat")
	tt.Frame()
	if !tt.HasText("Commands") || !tt.HasText("/status") {
		t.Fatalf("command menu missing: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.Contains(app.Notice, "Model: gpt-test") || !tt.HasText(app.Notice) {
		t.Fatalf("status notice %q not shown in %q", app.Notice, tt.Texts())
	}
}

func TestSidebarSelectsAndArchivesThreads(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Title: "First thread", Busy: true}, {ID: 2, Title: "Second thread", Agent: "claude-code", HistoryLoaded: true}})
	tt.Frame()
	if err := tt.Click("Second thread"); err != nil {
		t.Fatal(err)
	}
	if c.Active() != 2 || c.CurrentAgent() != "claude-code" {
		t.Fatalf("thread not selected: active %d agent %s", c.Active(), c.CurrentAgent())
	}
	tt.Move(1100, 780)
	if tt.HasText("Conversation actions") || !tt.HasText(appmodel.Tr("Working…")) {
		t.Fatal("run status should stay visible while conversation actions wait for hover")
	}
	first, _ := tt.Find("First thread")
	tt.Move(first.X+first.W/2, first.Y+first.H/2)
	if !tt.HasText("Conversation actions") {
		t.Fatal("hovering hid the more button")
	}
	if status, ok := tt.Find(appmodel.Tr("Working…")); ok && status.Y < first.Y+first.H && status.Y+status.H > first.Y {
		t.Fatal("the more icon should replace run status on hover")
	}
	if title, _ := tt.Find("First thread"); title != first {
		t.Fatal("hovering changed the title's layout")
	}
	if tt.HasText("Archive conversation") || tt.HasText("Delete conversation") {
		t.Fatal("destructive actions are exposed before opening the menu")
	}
	if err := tt.Click("Conversation actions"); err != nil {
		t.Fatal(err)
	}
	if len(c.Threads()) != 2 || c.Active() != 2 {
		t.Fatal("opening the actions menu changed the conversation")
	}
	if !tt.HasText("Archive conversation") || !tt.HasText("Delete conversation") {
		t.Fatalf("conversation actions are missing from the dropdown: %q", tt.Texts())
	}
	if err := tt.Click("Archive conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("First thread") || len(c.Threads()) != 1 {
		t.Fatalf("archived thread still listed: %q", tt.Texts())
	}
	completedTitle := strings.Repeat("已完成的长标题会话", 8)
	c.Update(2, func(thread *model.Thread) { thread.Title = completedTitle })
	tt.Move(1100, 780)
	second, ok := tt.Find(completedTitle)
	if !ok {
		t.Fatal("completed conversation is missing")
	}
	tt.Move(second.X+4, second.Y+second.H/2)
	if more, ok := tt.Find("Conversation actions"); !ok || more.Y >= second.Y+second.H || more.Y+more.H <= second.Y {
		t.Fatal("hovering a completed conversation should show its more icon")
	}
	if err := tt.Click("Conversation actions"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Archive conversation") || !tt.HasText("Delete conversation") {
		t.Fatal("completed conversation actions should open normally")
	}
}

func TestSettingsGeneralAndAccount(t *testing.T) {
	app, tt := testApp(t)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Appearance") || !tt.HasText("Full access") {
		t.Fatalf("general settings missing: %q", tt.Texts())
	}
	if err := tt.Click("Full access"); err != nil {
		t.Fatal(err)
	}
	if app.C.(*controller.Controller).PermissionPolicy() != "never" {
		t.Fatalf("default permission %q", app.C.(*controller.Controller).PermissionPolicy())
	}
	app.C.(*controller.Controller).SetLocale("zh")
	tt.Frame()
	if !tt.HasText("Appearance") || !tt.HasText("Interface language") {
		t.Fatalf("legacy locale changed the English interface: %q", tt.Texts())
	}
	if err := tt.Click("Account"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Connect to Codex and try again.") {
		t.Fatalf("account view without connection: %q", tt.Texts())
	}
}

func TestAccountDetailsAndResetCard(t *testing.T) {
	data := resetTestDisplay(t)
	attempt, err := account.NewResetAttempt(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []account.ResetAction{{}, {Attempt: attempt, Confirming: true}, {Attempt: attempt, Confirming: true, Busy: true}} {
		tt := ui.NewTester(func(c *ui.Context) {
			desktopui.AccountDetails(c, data)
			desktopui.ResetCreditCard(c, data, action, true, func() {}, func() {}, func() {})
		}, 800, 900)
		for _, want := range []string{"4 available", "Codex reset", "Usage"} {
			if !tt.HasText(want) {
				t.Fatalf("missing %q in %q", want, tt.Texts())
			}
		}
		if action.Confirming && !tt.HasText("Use a reset card?") {
			t.Fatalf("confirmation missing: %q", tt.Texts())
		}
	}
}

func TestProjectDialogValidatesFolder(t *testing.T) {
	app, tt := testApp(t)
	if err := tt.Click("Select project"); err != nil {
		t.Fatal(err)
	}
	if !app.ProjectDialog || !tt.HasText("Project folder") {
		t.Fatalf("dialog not open: %q", tt.Texts())
	}
	app.ProjectPath = "/definitely/not/a/folder"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !app.ProjectDialog || app.ProjectError == "" {
		t.Fatal("missing folder accepted")
	}
}

func TestComposerPopupChoicesAlignLeft(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetReady(true)
	app.C.(*controller.Controller).SetModels([]codex.Model{{Model: "gpt-a"}, {Model: "gpt-with-a-much-longer-name"}})
	tt.Frame()
	if err := tt.Click("gpt-test"); err != nil {
		t.Fatal(err)
	}
	short, ok1 := tt.Find("gpt-a")
	long, ok2 := tt.Find("gpt-with-a-much-longer-name")
	if !ok1 || !ok2 {
		t.Fatalf("model choices missing: %q", tt.Texts())
	}
	if d := short.X - long.X; d < -0.5 || d > 0.5 {
		t.Fatalf("choices not left aligned: %v vs %v", short, long)
	}
}
