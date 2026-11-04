package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/codex"

	"github.com/egoist/mygo/ui"
)

// testApp builds the app around a controller that has no Codex process, so
// views run without a window, as fast as a unit test.
func testApp(t *testing.T) (*chatApp, *ui.Tester) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	inlineDispatch.Store(true)
	t.Cleanup(func() { inlineDispatch.Store(false) })
	cwd := t.TempDir()
	c := &chatController{cwd: cwd, referenceSource: testReferenceSource}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t.Cleanup(c.cancel)
	c.agent, c.setAgent = createSignal("codex")
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.projects, c.setProjects = createSignal([]Project{{Name: "demo", Path: cwd}})
	c.threads, c.setThreads = createSignal([]Thread{})
	c.active, c.setActive = createSignal(0)
	c.draft, c.setDraft = createSignal("")
	c.model, c.setModel = createSignal("gpt-test")
	c.effort, c.setEffort = createSignal("medium")
	c.permissionPolicy, c.setPermissionPolicy = createSignal("on-request")
	c.locale, c.setLocale = createSignal("en")
	c.models, c.setModels = createSignal([]codex.Model{})
	c.attachments, c.setAttachments = createSignal([]string{})
	c.status, c.setStatus = createSignal("Connecting to Codex…")
	c.ready, c.setReady = createSignal(false)
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	c.historyStatus, c.setHistoryStatus = createSignal("")
	c.revision, c.setRevision = createSignal(0)
	app := &chatApp{
		c: c, theme: appTheme(), sidebar: true,
		workspace: cwd, projectName: "demo", projectPath: cwd, expandedProject: cwd,
		threadListExpanded: map[string]bool{}, lists: map[int]*ui.ListState{},
		copied: map[int]bool{}, open: map[int]bool{},
		answers: map[string]map[string]string{}, forms: map[string]string{}, formErrors: map[string]string{},
	}
	app.account.init(app)
	return app, ui.NewTester(app.view, 1180, 820)
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
	if !strings.HasPrefix(app.c.draft(), "Find and fix a bug") {
		t.Fatalf("suggestion did not fill the draft: %q", app.c.draft())
	}
}

func TestConversationStreamsAndOffersCopyAfterTurn(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, ServerID: "t1", Title: "Greeting", Loaded: true, HistoryLoaded: true, Resumed: true}})
	c.setActive(1)
	c.update(1, func(t *Thread) { t.Messages = []Message{{ID: 10, Role: "user", Content: "Say **hi**"}} })
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	c.handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"a","delta":"Hello "}`))
	tt.Frame()
	if !tt.HasText("Greeting") || !tt.HasText("hi") {
		t.Fatalf("conversation not shown: %q", tt.Texts())
	}
	if _, ok := tt.Find("Stop response"); !ok {
		t.Fatal("busy conversation has no stop button")
	}
	c.handle(event("item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"Hello world"}}`))
	c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"completed"}}`))
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
	c := app.c
	c.setThreads([]Thread{{ID: 1, ServerID: "t1", Title: "Tools", Loaded: true, HistoryLoaded: true}})
	c.setActive(1)
	c.update(1, func(t *Thread) {
		t.Messages = []Message{
			{ID: 1, Role: "user", Content: "run"},
			{ID: 2, Role: "tool", Kind: "commandExecution", Status: "completed", Item: &codex.Item{Command: "ls -la"}, Content: "out"},
			{ID: 3, Role: "tool", Kind: "commandExecution", Status: "failed", Item: &codex.Item{Command: "go test"}, Content: "FAIL"},
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

func TestApprovalCardResolvesRequest(t *testing.T) {
	app, tt := testApp(t)
	id, _ := json.Marshal("claude:1:a1")
	app.c.setApprovals([]codex.Approval{{ID: id, Method: "claude/permission", Command: "Bash", Reason: `{"command":"pwd"}`}})
	tt.Frame()
	if !tt.HasText("Claude Code requests approval") {
		t.Fatalf("approval missing: %q", tt.Texts())
	}
	if err := tt.Click("Decline"); err != nil {
		t.Fatal(err)
	}
	if len(app.c.approvals()) != 0 || tt.HasText("Claude Code requests approval") {
		t.Fatal("declined approval still pending")
	}
}

func TestQuestionNeedsEveryAnswer(t *testing.T) {
	app, tt := testApp(t)
	id, _ := json.Marshal("claude:1:q1")
	app.c.setApprovals([]codex.Approval{{ID: id, Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q", Question: "Pick a color", Options: []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	}{{Label: "Blue", Description: "calm"}}}}}})
	tt.Frame()
	if err := tt.Click("Submit answers"); err != nil {
		t.Fatal(err)
	}
	if len(app.c.approvals()) != 1 {
		t.Fatal("submitted without an answer")
	}
	if err := tt.Click("Blue · calm"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Submit answers"); err != nil {
		t.Fatal(err)
	}
	if len(app.c.approvals()) != 0 {
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
	if !strings.Contains(app.notice, "Model: gpt-test") || !tt.HasText(app.notice) {
		t.Fatalf("status notice %q not shown in %q", app.notice, tt.Texts())
	}
}

func TestSidebarSelectsAndArchivesThreads(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "First thread", Busy: true}, {ID: 2, Title: "Second thread", Agent: "claude-code", HistoryLoaded: true}})
	tt.Frame()
	if err := tt.Click("Second thread"); err != nil {
		t.Fatal(err)
	}
	if c.active() != 2 || c.currentAgent() != "claude-code" {
		t.Fatalf("thread not selected: active %d agent %s", c.active(), c.currentAgent())
	}
	tt.Move(1100, 780)
	if tt.HasText("Conversation actions") || !tt.HasText(tr("Working…")) {
		t.Fatal("unhovered conversations should only show run status")
	}
	first, _ := tt.Find("First thread")
	tt.Move(first.X+first.W/2, first.Y+first.H/2)
	if !tt.HasText("Conversation actions") {
		t.Fatal("hovering should replace run status with the more button")
	}
	// The same conversation also appears under Projects. Its other row
	// keeps running status while the activity row shows its actions.
	if status, ok := tt.Find(tr("Working…")); ok && status.Y < first.Y+first.H && status.Y+status.H > first.Y {
		t.Fatal("the hovered row still shows run status")
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
	if len(c.threads()) != 2 || c.active() != 2 {
		t.Fatal("opening the actions menu changed the conversation")
	}
	if !tt.HasText("Archive conversation") || !tt.HasText("Delete conversation") {
		t.Fatalf("conversation actions are missing from the dropdown: %q", tt.Texts())
	}
	if err := tt.Click("Archive conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("First thread") || len(c.threads()) != 1 {
		t.Fatalf("archived thread still listed: %q", tt.Texts())
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
	if app.c.permissionPolicy() != "never" {
		t.Fatalf("default permission %q", app.c.permissionPolicy())
	}
	app.c.setLocale("zh")
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
	attempt, err := newAccountResetAttempt(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []accountResetAction{{}, {Attempt: attempt, Confirming: true}, {Attempt: attempt, Confirming: true, Busy: true}} {
		tt := ui.NewTester(func(c *ui.Context) {
			accountDetails(c, data)
			resetCreditCard(c, data, action, true, func() {}, func() {}, func() {})
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
	if !app.projectDialog || !tt.HasText("Project folder") {
		t.Fatalf("dialog not open: %q", tt.Texts())
	}
	app.projectPath = "/definitely/not/a/folder"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !app.projectDialog || app.projectError == "" {
		t.Fatal("missing folder accepted")
	}
}

func TestComposerPopupChoicesAlignLeft(t *testing.T) {
	app, tt := testApp(t)
	app.c.setReady(true)
	app.c.setModels([]codex.Model{{Model: "gpt-a"}, {Model: "gpt-with-a-much-longer-name"}})
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
