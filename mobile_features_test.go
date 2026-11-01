package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/remote"
)

func TestMobileModelSettingsRemainLocalAndKeepDraft(t *testing.T) {
	m := testMobileApp()
	m.startConversation("/a", agentCodex)
	m.dataDir = t.TempDir()
	m.draft = "未发送的内容"
	m.state.Agents = []phoneAgentOptions{{Agent: agentCodex, Current: remote.SendOptions{Model: "first", Effort: "low", Policy: "on-request"}, Models: []phoneModelOption{
		{Value: "first", Label: "First", Efforts: []string{"low"}, DefaultEffort: "low"},
		{Value: "second", Label: "Second", Efforts: []string{"medium", "high"}, DefaultEffort: "medium"},
	}}}
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("模型设置"); err != nil {
		t.Fatal(err)
	}
	if m.router().Path() != "/options" {
		t.Fatal("settings page did not open")
	}
	if err := tt.Click("模型 Second"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("强度 HIGH"); err != nil {
		t.Fatal(err)
	}
	if got := m.sendOptions(agentCodex); got.Model != "second" || got.Effort != "high" {
		t.Fatalf("choices not applied: %+v", got)
	}
	if m.state.Agents[0].Current.Model != "first" {
		t.Fatal("phone changed desktop defaults")
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	if m.draft != "未发送的内容" {
		t.Fatal("settings navigation lost the draft")
	}
	data, err := os.ReadFile(filepath.Join(m.dataDir, "message-options.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]remote.SendOptions
	if json.Unmarshal(data, &saved) != nil || saved[agentCodex].Model != "second" {
		t.Fatal("choice was not persisted")
	}
}

func TestMobileBusyComposerOffersQueueAndSteer(t *testing.T) {
	m := testMobileApp()
	th := Thread{ID: 1, ServerID: "s1", Busy: true, HistoryLoaded: true}
	m.openThread(th)
	m.showThread(th)
	m.draft = "下一步"
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	for _, label := range []string{"加入队列", "立即引导"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("missing %s", label)
		}
	}
	if _, ok := tt.Find("停止"); ok {
		t.Fatal("a draft should be sent instead of stopping the turn")
	}
	m.draft = ""
	tt.Frame()
	if _, ok := tt.Find("停止"); !ok {
		t.Fatal("empty composer should still stop a turn")
	}
}

func TestMobileQuestionPageAcceptsOptionsAndKeepsFailedAnswer(t *testing.T) {
	m := testMobileApp()
	a := codex.Approval{ID: json.RawMessage(`1`), ThreadID: "s1", Method: "item/tool/requestUserInput"}
	if err := json.Unmarshal([]byte(`[{"id":"choice","header":"方案","question":"选择哪一个？","options":[{"label":"方案 A","description":"保留现有行为"}]}]`), &a.Questions); err != nil {
		t.Fatal(err)
	}
	m.state.Approvals = []codex.Approval{a}
	m.openThread(m.state.Projects[0].Threads[0])
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("填写回答"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("方案 A"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("提交回答"); err != nil {
		t.Fatal(err)
	}
	if m.fieldsFor(a)["choice"] != "方案 A" || len(m.state.Approvals) != 1 || m.view.formErrors["1"] == "" {
		t.Fatal("failed/offline submission lost the answer or card")
	}
	m.approvalAnswered(a.ID, "")
	if len(m.state.Approvals) != 0 || m.router().Path() != "/thread" {
		t.Fatal("acknowledged response did not close the form")
	}
}

func TestMobileFormBuildsTypedValues(t *testing.T) {
	a := codex.Approval{RequestedSchema: json.RawMessage(`{"type":"object","required":["name","count","enabled","mode"],"properties":{"name":{"type":"string"},"count":{"type":"integer"},"enabled":{"type":"boolean"},"mode":{"type":"string","enum":["a","b"]}}}`)}
	values, err := validateMobileForm(a, map[string]string{"name": "hello", "count": "2", "enabled": "false", "mode": `"b"`})
	if err != nil || values["count"] != float64(2) || values["enabled"] != false || values["mode"] != "b" {
		t.Fatalf("wrong form types: %+v %v", values, err)
	}
	for _, fields := range []map[string]string{{"name": "hello"}, {"name": "hello", "count": "2.5", "enabled": "false", "mode": `"b"`}} {
		if _, err := validateMobileForm(a, fields); err == nil {
			t.Fatal("incomplete or invalid form was accepted")
		}
	}
}

func TestPhoneApprovalValidatesBeforeRemovingCard(t *testing.T) {
	c := testController()
	a := codex.Approval{ID: json.RawMessage(`1`), Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q", Question: "名字？"}}}
	c.setApprovals([]codex.Approval{a})
	if err := c.phoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"answers":{"wrong":{"answers":["x"]}}}`)}); err == nil || len(c.approvals()) != 1 {
		t.Fatal("invalid answer consumed the request")
	}
	if err := c.phoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"answers":{"q":{"answers":["hello"]}}}`)}); err != nil || len(c.approvals()) != 0 {
		t.Fatal("valid answer was refused", err)
	}
	if err := c.phoneApprovalResponse(remote.Request{Approval: a.ID, Allow: true}); err == nil {
		t.Fatal("stale request was accepted")
	}
	a = codex.Approval{ID: json.RawMessage(`2`), Method: "mcpServer/elicitation/request", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"]}`)}
	c.setApprovals([]codex.Approval{a})
	if err := c.phoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"action":"accept","content":{"enabled":"true"}}`)}); err == nil || len(c.approvals()) != 1 {
		t.Fatal("invalid MCP form was accepted")
	}
	if err := c.phoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"action":"accept","content":{"enabled":true}}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestPhoneApprovalRejectsReusedRequestID(t *testing.T) {
	c := testController()
	old := codex.Approval{ID: json.RawMessage(`1`), Method: "item/commandExecution/requestApproval", Command: "safe command"}
	next := old
	next.Command = "different command"
	c.setApprovals([]codex.Approval{next})
	if err := c.phoneApprovalResponse(remote.Request{Approval: old.ID, ApprovalVersion: approvalFingerprint(old), Allow: true}); err == nil || len(c.approvals()) != 1 {
		t.Fatal("a reused request ID accepted an old approval")
	}
	m := testMobileApp()
	m.state.Approvals = []codex.Approval{old}
	m.fieldsFor(old)["secret"] = "unsent secret"
	m.applyPhoneState(phoneState{Approvals: []codex.Approval{next}})
	if len(m.fieldsFor(next)) != 0 {
		t.Fatal("a new request inherited the previous request's private input")
	}
}
