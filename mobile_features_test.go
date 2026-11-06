package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	controller "miya/internal/controller"
	remote "miya/internal/remote"
	widgets "miya/internal/widgets"
	os "os"
	filepath "path/filepath"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func TestMobileModelSettingsRemainLocalAndKeepDraft(t *testing.T) {
	m := testMobileApp()
	m.StartConversation("/a", appmodel.AgentCodex)
	m.DataDir = t.TempDir()
	m.Draft = "未发送的内容"
	m.State.Agents = []appmodel.PhoneAgentOptions{{Agent: appmodel.AgentCodex, Current: remote.SendOptions{Model: "first", Effort: "low", Policy: "on-request"}, Models: []appmodel.PhoneModelOption{
		{Value: "first", Label: "First", Efforts: []string{"low"}, DefaultEffort: "low"},
		{Value: "second", Label: "Second", Efforts: []string{"medium", "high"}, DefaultEffort: "medium"},
	}}}
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("Model settings"); err != nil {
		t.Fatal(err)
	}
	if m.Router().Path() != "/options" {
		t.Fatal("settings page did not open")
	}
	if err := tt.Click("Model Second"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Effort HIGH"); err != nil {
		t.Fatal(err)
	}
	if got := m.SendOptions(appmodel.AgentCodex); got.Model != "second" || got.Effort != "high" {
		t.Fatalf("choices not applied: %+v", got)
	}
	if m.State.Agents[0].Current.Model != "first" {
		t.Fatal("phone changed desktop defaults")
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if m.Draft != "未发送的内容" {
		t.Fatal("settings navigation lost the draft")
	}
	data, err := os.ReadFile(filepath.Join(m.DataDir, "message-options.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]remote.SendOptions
	if json.Unmarshal(data, &saved) != nil || saved[appmodel.AgentCodex].Model != "second" {
		t.Fatal("choice was not persisted")
	}
}

func TestMobileBusyComposerOffersQueueAndSteer(t *testing.T) {
	m := testMobileApp()
	th := appmodel.Thread{ID: 1, ServerID: "s1", Busy: true, HistoryLoaded: true}
	m.OpenThread(th)
	m.ShowThread(th)
	m.Draft = "下一步"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	for _, label := range []string{"Add to queue", "Steer now"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("missing %s", label)
		}
	}
	if _, ok := tt.Find("Stop"); ok {
		t.Fatal("a draft should be sent instead of stopping the turn")
	}
	m.Draft = ""
	tt.Frame()
	if _, ok := tt.Find("Stop"); !ok {
		t.Fatal("empty composer should still stop a turn")
	}
}

func TestMobileQuestionPageAcceptsOptionsAndKeepsFailedAnswer(t *testing.T) {
	m := testMobileApp()
	a := codex.Approval{ID: json.RawMessage(`1`), ThreadID: "s1", Method: "item/tool/requestUserInput"}
	if err := json.Unmarshal([]byte(`[{"id":"choice","header":"方案","question":"选择哪一个？","options":[{"label":"方案 A","description":"保留现有行为"}]}]`), &a.Questions); err != nil {
		t.Fatal(err)
	}
	m.State.Approvals = []codex.Approval{a}
	m.OpenThread(m.State.Projects[0].Threads[0])
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("Enter your answer"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("方案 A"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Submit answer"); err != nil {
		t.Fatal(err)
	}
	if m.FieldsFor(a)["choice"] != "方案 A" || len(m.State.Approvals) != 1 || m.FormErrors["1"] == "" {
		t.Fatal("failed/offline submission lost the answer or card")
	}
	m.ApprovalAnswered(a.ID, "")
	if len(m.State.Approvals) != 0 || m.Router().Path() != "/thread" {
		t.Fatal("acknowledged response did not close the form")
	}
}

func TestMobileFormBuildsTypedValues(t *testing.T) {
	a := codex.Approval{RequestedSchema: json.RawMessage(`{"type":"object","required":["name","count","enabled","mode"],"properties":{"name":{"type":"string"},"count":{"type":"integer"},"enabled":{"type":"boolean"},"mode":{"type":"string","enum":["a","b"]}}}`)}
	values, err := appmodel.ValidateMobileForm(a, map[string]string{"name": "hello", "count": "2", "enabled": "false", "mode": `"b"`})
	if err != nil || values["count"] != float64(2) || values["enabled"] != false || values["mode"] != "b" {
		t.Fatalf("wrong form types: %+v %v", values, err)
	}
	for _, fields := range []map[string]string{{"name": "hello"}, {"name": "hello", "count": "2.5", "enabled": "false", "mode": `"b"`}} {
		if _, err := appmodel.ValidateMobileForm(a, fields); err == nil {
			t.Fatal("incomplete or invalid form was accepted")
		}
	}
}

func TestPhoneApprovalValidatesBeforeRemovingCard(t *testing.T) {
	c := testController()
	a := codex.Approval{ID: json.RawMessage(`1`), Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q", Question: "名字？"}}}
	c.SetApprovals([]codex.Approval{a})
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"answers":{"wrong":{"answers":["x"]}}}`)}); err == nil || len(c.Approvals()) != 1 {
		t.Fatal("invalid answer consumed the request")
	}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"answers":{"q":{"answers":["hello"]}}}`)}); err != nil || len(c.Approvals()) != 0 {
		t.Fatal("valid answer was refused", err)
	}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Allow: true}); err == nil {
		t.Fatal("stale request was accepted")
	}
	a = codex.Approval{ID: json.RawMessage(`2`), Method: "mcpServer/elicitation/request", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"]}`)}
	c.SetApprovals([]codex.Approval{a})
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"action":"accept","content":{"enabled":"true"}}`)}); err == nil || len(c.Approvals()) != 1 {
		t.Fatal("invalid MCP form was accepted")
	}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"action":"accept","content":{"enabled":true}}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestPhoneApprovalRejectsReusedRequestID(t *testing.T) {
	c := testController()
	old := codex.Approval{ID: json.RawMessage(`1`), Method: "item/commandExecution/requestApproval", Command: "safe command"}
	next := old
	next.Command = "different command"
	c.SetApprovals([]codex.Approval{next})
	if err := c.PhoneApprovalResponse(remote.Request{Approval: old.ID, ApprovalVersion: appmodel.ApprovalFingerprint(old), Allow: true}); err == nil || len(c.Approvals()) != 1 {
		t.Fatal("a reused request ID accepted an old approval")
	}
	m := testMobileApp()
	m.State.Approvals = []codex.Approval{old}
	m.FieldsFor(old)["secret"] = "unsent secret"
	m.ApplyPhoneState(appmodel.PhoneState{Approvals: []codex.Approval{next}})
	if len(m.FieldsFor(next)) != 0 {
		t.Fatal("a new request inherited the previous request's private input")
	}
}

func TestMobileAgentTasksToggleSharesWorkingRow(t *testing.T) {
	m := testMobileApp()
	m.State.Collaboration = &appmodel.CollaborationStatus{Version: 1}
	th := appmodel.Thread{ID: 1, ServerID: "s1", Busy: true, HistoryLoaded: true, StartedAtMs: time.Now().Add(-10 * time.Second).UnixMilli()}
	m.OpenThread(th)
	m.ShowThread(th)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if tt.HasText("Agent tasks") {
		t.Fatal("toggle shown without any Agent tasks")
	}
	pane, _, _ := m.CollaborationPane()
	pane.Loaded, pane.Rows = true, []widgets.CollaborationTaskView{{ID: "t1", Title: "Review", Agent: appmodel.AgentCodex}}
	tt.Frame()
	working, ok := tt.Find(appmodel.ThreadWorkingLabel(m.Viewer.Current()))
	if !ok {
		t.Fatal("missing working status")
	}
	toggle, ok := tt.Find("Agent tasks (1)")
	if !ok {
		t.Fatal("missing Agent tasks toggle")
	}
	if mid := working.Y + working.H/2; mid < toggle.Y || mid > toggle.Y+toggle.H {
		t.Fatalf("toggle %v not on the working row %v", toggle, working)
	}
	if toggle.X+toggle.W < 390-24 {
		t.Fatalf("toggle %v not at the right edge", toggle)
	}
	count := 0
	for _, s := range tt.Texts() {
		if s == "Agent tasks (1)" {
			count++
		}
	}
	// One button contributes its accessibility label and its text.
	if count != 2 {
		t.Fatalf("toggle drawn %d times", count/2)
	}
}

func TestDesktopAgentTasksToggleSharesWorkingRow(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).CollaborationRemote = &appmodel.CollaborationStatus{Version: 1}
	app.C.(*controller.Controller).SetThreads([]appmodel.Thread{{ID: 1, ServerID: "s1", Title: "Running", Busy: true, HistoryLoaded: true, StartedAtMs: time.Now().Add(-10 * time.Second).UnixMilli()}})
	app.C.(*controller.Controller).SetActive(1)
	tt.Frame()
	if tt.HasText("Agent tasks") {
		t.Fatal("toggle shown without any Agent tasks")
	}
	pane, _, _ := app.CollaborationPane(app.C.(*controller.Controller).Current())
	pane.Loaded, pane.Rows = true, []widgets.CollaborationTaskView{{ID: "t1", Title: "Review", Agent: appmodel.AgentCodex}}
	tt.Frame()
	working, ok := tt.Find(appmodel.ThreadWorkingLabel(app.C.(*controller.Controller).Current()))
	if !ok {
		t.Fatal("missing working status", tt.Texts())
	}
	toggle, ok := tt.Find("Agent tasks (1)")
	if !ok {
		t.Fatal("missing Agent tasks toggle")
	}
	if mid := working.Y + working.H/2; mid < toggle.Y || mid > toggle.Y+toggle.H {
		t.Fatalf("toggle %v not on the working row %v", toggle, working)
	}
	if toggle.X < working.X+working.W+100 {
		t.Fatalf("toggle %v not at the right edge of the row %v", toggle, working)
	}
	count := 0
	for _, s := range tt.Texts() {
		if s == "Agent tasks (1)" {
			count++
		}
	}
	// One button contributes its accessibility label and its text.
	if count != 2 {
		t.Fatalf("toggle drawn %d times", count/2)
	}
}
