package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/automation"

	"github.com/egoist/mygo/ui"
)

func mobileAutomationFixture() automation.View {
	return automation.ViewOf(automation.Automation{ID: "daily", Name: "仓库日报", Project: "/a", Agent: agentCodex, Instructions: "总结仓库的最新变化。", Schedule: "0 18 * * *", Enabled: true}, []automation.Run{{Started: atLocal("2026-11-01 18:00"), Thread: 99, ServerID: "s1", Result: automation.Passed, Summary: "日报已完成", Report: &automation.Report{Summary: "已完成检查", Findings: []string{"测试全部通过"}}}}, false, atLocal("2026-11-01 19:00"))
}

func TestMobileAutomationCreateNavigationAndFeedback(t *testing.T) {
	m := testMobileApp()
	m.state.Automations, m.state.AutomationCreate = true, true
	m.autos.loaded = true
	m.router().Push("/automations")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	if err := tt.Click("New automation"); err != nil {
		t.Fatal(err)
	}
	f := &m.autos.form
	if m.router().Path() != "/automation/new" || f.project != "/a" || !f.enabled || f.agent != agentCodex {
		t.Fatal("new form did not inherit the selected desktop project")
	}
	if err := tt.Click("What to do"); err != nil {
		t.Fatal(err)
	}
	tt.Type("每天 18 点总结仓库变化")
	tt.Frame()
	if f.request != "每天 18 点总结仓库变化" || !tt.HasText("Generate settings") {
		t.Fatal("form request is not editable")
	}
	v := mobileAutomationFixture()
	f.pending, f.op = "draft", "automation-draft"
	raw, _ := json.Marshal(phoneAutomationFormResult{RequestID: "stale", View: &v})
	m.receiveAutomationForm("automation-draft", raw, "")
	if f.draft != nil || f.pending != "draft" {
		t.Fatal("stale draft replaced the current request")
	}
	raw, _ = json.Marshal(phoneAutomationFormResult{RequestID: "draft", View: &v})
	m.receiveAutomationForm("automation-draft", raw, "")
	tt.Frame()
	if f.draft == nil || f.pending != "" || !tt.HasText("Generated settings") || !tt.HasText("Create") {
		t.Fatal("draft preview not shown")
	}
	automationSnapshot(t, tt, "mobile-automation-create-preview")
	f.pending, f.op, f.createKey = "0011223344556677", "automation-create", "0011223344556677"
	f.createPayload = json.RawMessage(`{"name":"saved"}`)
	m.interruptAutomationForm()
	tt.Frame()
	if f.pending != "" || f.createKey == "" || !tt.HasText("Retry creation") || !tt.HasText("Disconnected. Reconnect to try again.") {
		t.Fatal("interrupted creation lost its retry identity")
	}
	f.pending = f.createKey
	raw, _ = json.Marshal(phoneAutomationFormResult{RequestID: f.pending, View: &v})
	m.receiveAutomationForm("automation-create", raw, "")
	tt.Frame()
	if m.router().Path() != "/automation" || m.autos.detail != v.ID || len(m.autos.list) != 1 || f.createKey != "" || !tt.HasText(v.Name) {
		t.Fatal("saved automation did not open its desktop-backed detail")
	}
}

func TestMobileAutomationCreateCompatibilityAndSmallScreens(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.state.Automations = true
		m.autos.loaded = true
		m.router().Push("/automations")
		tt := ui.NewTester(m.viewRoot, width, 844)
		tt.Frame()
		if !tt.HasText("Update the desktop app to create automations on your phone") {
			t.Fatal("old desktop did not explain the missing creation capability")
		}
		if err := tt.Click("New automation"); err == nil && m.router().Path() != "/automations" {
			t.Fatal("old desktop opened an unsupported form")
		}
		m.state.AutomationCreate = true
		m.openAutomationCreate()
		v := mobileAutomationFixture()
		m.autos.form.draft = &v
		m.autos.form.request = "每天 18 点总结仓库变化"
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			tt.SetPreferences(ui.Preferences{TextScale: 1.6})
			tt.Frame()
			for _, label := range []string{"Back", "Automation project", "What to do", "Create"} {
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.X+r.W > float32(width) || r.H < 44 {
					t.Fatalf("creation control %q is clipped: %v", label, r)
				}
			}
			automationSnapshot(t, tt, fmt.Sprintf("mobile-automation-create-%d-dark-%v", width, dark))
			tt.Scroll(float32(width/2), 500, 0, 1200)
			tt.Frame()
			automationSnapshot(t, tt, fmt.Sprintf("mobile-automation-create-bottom-%d-dark-%v", width, dark))
			tt.Scroll(float32(width/2), 500, 0, -2000)
		}
	}
}

func TestMobileAutomationsNavigationAndRun(t *testing.T) {
	m := testMobileApp()
	m.state.Automations = true
	v := mobileAutomationFixture()
	data, _ := json.Marshal([]automation.View{v})
	m.receiveAutomations(data, "")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("Automations"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.router().Path() != "/automations" || m.menu || !tt.HasText("仓库日报") {
		t.Fatal("drawer did not open the automation list")
	}
	if err := tt.Click("仓库日报"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Automation details", "总结仓库的最新变化。", "Run history", "日报已完成", "Open conversation"} {
		if !tt.HasText(text) {
			t.Fatalf("detail missing %q: %v", text, tt.Texts())
		}
	}
	if tt.HasText("测试全部通过") {
		t.Fatal("full results should stay collapsed until requested")
	}
	if err := tt.Click("View result"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("测试全部通过") || !tt.HasText("已完成检查") {
		t.Fatal("expanding the result lost the structured report")
	}
	automationSnapshot(t, tt, "mobile-automation-expanded")
	if err := tt.Click("Collapse result"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Open run conversation"); err != nil {
		t.Fatal(err)
	}
	if m.open != 1 || m.router().Path() != "/thread" {
		t.Fatal("run did not open its renumbered conversation")
	}
	openMobileDrawer(t, tt)
	if m.router().Path() != "/thread" || m.open != 1 || !m.menu {
		t.Fatal("the conversation's arrow should open the drawer without leaving the run")
	}
	if err := tt.Click("Automations"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.router().Path() != "/automations" || m.open != 0 {
		t.Fatal("the drawer should return to automations and unsubscribe the conversation")
	}
	if err := tt.Click("仓库日报"); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		if dir := os.Getenv("MIYA_SNAPSHOT"); dir != "" {
			name := "mobile-automation-light.png"
			if dark {
				name = "mobile-automation-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMobileAutomationsUnavailableAndReadErrors(t *testing.T) {
	m := testMobileApp()
	m.router().Push("/automations")
	tt := ui.NewTester(m.viewRoot, 320, 568)
	tt.Frame()
	if !tt.HasText("Desktop update required") {
		t.Fatal("old desktop should explain that an update is needed")
	}
	m.state.Automations = true
	m.receiveAutomations(json.RawMessage(`[]`), "")
	tt.Frame()
	if !tt.HasText("No automations yet") {
		t.Fatal("empty automation state missing")
	}
	v := mobileAutomationFixture()
	data, _ := json.Marshal([]automation.View{v})
	m.receiveAutomations(data, "")
	m.receiveAutomations(nil, "Failed to load")
	m.receiveAutomations(json.RawMessage(`broken`), "")
	if len(m.autos.list) != 1 || m.autos.loading || m.autos.err == "" {
		t.Fatal("a failed read lost the last successful list")
	}
	m.connected = false
	tt.Frame()
	if !tt.HasText("Disconnected. Showing the last synced content.") || !tt.HasText(v.Name) {
		t.Fatal("offline state should retain the last read list")
	}
	m.autos.loading, m.autos.requestedAt = true, time.Now().Add(-11*time.Second)
	tt.Frame()
	if m.autos.loading || !tt.HasText("Read timed out. Try again.") {
		t.Fatal("a missing response should time out")
	}
	m.forget()
	if len(m.autos.list) != 0 {
		t.Fatal("unpairing retained another desktop's automations")
	}
}

func TestMobileAutomationRunDoesNotOpenReusedThreadID(t *testing.T) {
	m := testMobileApp()
	v := mobileAutomationFixture()
	r := v.Runs[0]
	r.Thread, r.ServerID = 1, "deleted"
	if _, ok := m.automationRunThread(v, r); ok {
		t.Fatal("a stale run opened a reused numeric thread ID")
	}
	r.ServerID = "s1"
	v.Project = "/b"
	if _, ok := m.automationRunThread(v, r); ok {
		t.Fatal("a run opened a conversation in another project")
	}
	v.Project, v.Agent = "/a", agentClaude
	if _, ok := m.automationRunThread(v, r); ok {
		t.Fatal("a run opened another agent's conversation")
	}
}

func TestMobileAutomationRunFeedbackAndCompatibility(t *testing.T) {
	m := testMobileApp()
	m.state.Automations = true
	v := mobileAutomationFixture()
	m.autos.list, m.autos.loaded, m.autos.detail = []automation.View{v}, true, v.ID
	m.router().Push("/automation")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("Run now") || !tt.HasText("Update the desktop app to run now") {
		t.Fatal("old desktop did not explain manual run support")
	}
	if err := tt.Click("Run now"); err != nil {
		t.Fatal(err)
	}
	if m.autos.runRequest != "" {
		t.Fatal("unsupported desktop received a run request")
	}
	m.state.AutomationRun = true
	m.autos.runID, m.autos.runRequest, m.autos.runRequestedAt = v.ID, "request", time.Now()
	tt.Frame()
	if !tt.HasText("Starting…") || tt.HasText("Update the desktop app to run now") {
		t.Fatal("pending run feedback missing")
	}
	v.Running = true
	data, _ := json.Marshal(phoneAutomationRunResult{RequestID: "stale", List: []automation.View{v}})
	m.receiveAutomationRun(data, "")
	if m.autos.runRequest != "request" || m.autos.list[0].Running {
		t.Fatal("stale acknowledgement changed a pending request")
	}
	data, _ = json.Marshal(phoneAutomationRunResult{RequestID: "request", List: []automation.View{v}})
	m.receiveAutomationRun(data, "")
	tt.Frame()
	if m.autos.runRequest != "" || !m.autos.list[0].Running || !tt.HasText("Running") {
		t.Fatal("acknowledgement did not update the run status")
	}
	v.Running = false
	m.autos.runRequest = "failed"
	data, _ = json.Marshal(phoneAutomationRunResult{RequestID: "failed", List: []automation.View{v}})
	m.receiveAutomationRun(data, "Project directory does not exist")
	tt.Frame()
	if !tt.HasText("Project directory does not exist") || m.autos.runRequest != "" {
		t.Fatal("run failure was not shown")
	}
	m.autos.runRequest, m.autos.runRequestedAt = "timeout", time.Now().Add(-31*time.Second)
	tt.Frame()
	if m.autos.runRequest != "" || !tt.HasText("Request timed out. Refresh to check run status.") {
		t.Fatal("run timeout did not release the pending request")
	}
	m.autos.runRequest = "disconnect"
	m.disconnect()
	tt.Frame()
	if m.autos.runRequest != "" || !tt.HasText("Disconnected. Refresh to check run status.") {
		t.Fatal("disconnect left the run request pending")
	}
}

func TestMobileAutomationsDrawerLayout(t *testing.T) {
	m := mobileHomeFixture()
	tt := ui.NewTester(m.viewRoot, 844, 390)
	tt.SetSafeArea(ui.SafeArea{Left: 59, Right: 59, Bottom: 21})
	tt.Frame()
	m.menu = true
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	automationSnapshot(t, tt, "mobile-automation-drawer-opening")
	tt.Advance(time.Second)
	automationSnapshot(t, tt, "mobile-automation-drawer")
	if _, ok := tt.Find("Automations"); !ok {
		t.Fatal("automation entry missing from landscape drawer")
	}
}

func TestMobileAutomationsLongContentAndTextScaling(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, dark := range []bool{false, true} {
			m := testMobileApp()
			m.state.Automations = true
			v := mobileAutomationFixture()
			v.Name = "每日检查仓库 Issue 和持续集成结果"
			v.Instructions = strings.Repeat("检查新 Issue 和测试结果，记录需要处理的问题。", 12)
			paused := v
			paused.ID, paused.Name, paused.Enabled = "paused", "仓库日报", false
			running := v
			running.ID, running.Name, running.Running = "running", "检查部署状态", true
			m.autos.list, m.autos.loaded = []automation.View{v, paused, running}, true
			m.router().Push("/automations")
			tt := ui.NewTester(m.viewRoot, width, 844)
			tt.SetDark(dark)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			tt.Frame()
			name := fmt.Sprintf("mobile-automation-list-%d-dark-%v", width, dark)
			automationSnapshot(t, tt, name)
			tt.SetPreferences(ui.Preferences{TextScale: 1.6})
			for _, label := range []string{v.Name, "Open sidebar", "Refresh automations"} {
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.X+r.W > float32(width) || r.H < 44 {
					t.Fatalf("%q is clipped or too small at large text size: %v", label, r)
				}
			}
			automationSnapshot(t, tt, name+"-large")
			tt.SetPreferences(ui.Preferences{TextScale: 1})
			if err := tt.Click(v.Name); err != nil {
				t.Fatal(err)
			}
			preview, ok := tt.Find(v.Instructions)
			if !ok {
				t.Fatal("task preview missing")
			}
			automationSnapshot(t, tt, fmt.Sprintf("mobile-automation-long-%d-dark-%v", width, dark))
			if err := tt.Click("Expand instructions"); err != nil {
				t.Fatal(err)
			}
			full, _ := tt.Find(v.Instructions)
			if !m.autos.instructionsOpen || full.H <= preview.H {
				t.Fatal("task expansion did not reveal the full instructions")
			}
			if err := tt.Click("Collapse instructions"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
