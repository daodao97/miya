package integration

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/phoneapi"
)

func mobileAutomationFixture() automation.View {
	return automation.ViewOf(automation.Automation{ID: "daily", Name: "仓库日报", Project: "/a", Agent: appmodel.AgentCodex, Instructions: "总结仓库的最新变化。", Schedule: "0 18 * * *", Enabled: true}, []automation.Run{{Started: atLocal("2026-11-01 18:00"), Thread: 99, ServerID: "s1", Result: automation.Passed, Summary: "日报已完成", Report: &automation.Report{Summary: "已完成检查", Findings: []string{"测试全部通过"}}}}, false, atLocal("2026-11-01 19:00"))
}

func TestMobileAutomationCreateNavigationAndFeedback(t *testing.T) {
	m := testMobileApp()
	m.State.Automations, m.State.AutomationCreate = true, true
	m.Autos.Loaded = true
	m.Router().Push("/automations")
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	if err := tt.Click("New automation"); err != nil {
		t.Fatal(err)
	}
	f := &m.Autos.Form
	if m.Router().Path() != "/automation/new" || f.Project != "/a" || !f.Enabled || f.Agent != appmodel.AgentCodex {
		t.Fatal("new form did not inherit the selected desktop project")
	}
	if err := tt.Click("What to do"); err != nil {
		t.Fatal(err)
	}
	tt.Type("每天 18 点总结仓库变化")
	tt.Frame()
	if f.Request != "每天 18 点总结仓库变化" || !tt.HasText("Generate settings") {
		t.Fatal("form request is not editable")
	}
	v := mobileAutomationFixture()
	f.Pending, f.Op = "draft", "automation-draft"
	raw, _ := json.Marshal(phoneapi.AutomationFormResult{RequestID: "stale", View: &v})
	m.ReceiveAutomationForm("automation-draft", raw, "")
	if f.Draft != nil || f.Pending != "draft" {
		t.Fatal("stale draft replaced the current request")
	}
	raw, _ = json.Marshal(phoneapi.AutomationFormResult{RequestID: "draft", View: &v})
	m.ReceiveAutomationForm("automation-draft", raw, "")
	tt.Frame()
	if f.Draft == nil || f.Pending != "" || !tt.HasText("Generated settings") || !tt.HasText("Create") {
		t.Fatal("draft preview not shown")
	}
	automationSnapshot(t, tt, "mobile-automation-create-preview")
	f.Pending, f.Op, f.CreateKey = "0011223344556677", "automation-create", "0011223344556677"
	f.CreatePayload = json.RawMessage(`{"name":"saved"}`)
	m.InterruptAutomationForm()
	tt.Frame()
	if f.Pending != "" || f.CreateKey == "" || !tt.HasText("Retry creation") || !tt.HasText("Disconnected. Reconnect to try again.") {
		t.Fatal("interrupted creation lost its retry identity")
	}
	f.Pending = f.CreateKey
	raw, _ = json.Marshal(phoneapi.AutomationFormResult{RequestID: f.Pending, View: &v})
	m.ReceiveAutomationForm("automation-create", raw, "")
	tt.Frame()
	if m.Router().Path() != "/automation" || m.Autos.Detail != v.ID || len(m.Autos.List) != 1 || f.CreateKey != "" || !tt.HasText(v.Name) {
		t.Fatal("saved automation did not open its desktop-backed detail")
	}
}

func TestMobileAutomationCreateCompatibilityAndSmallScreens(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.State.Automations = true
		m.Autos.Loaded = true
		m.Router().Push("/automations")
		tt := ui.NewTester(m.ViewRoot, width, 844)
		tt.Frame()
		if !tt.HasText("Update the desktop app to create automations on your phone") {
			t.Fatal("old desktop did not explain the missing creation capability")
		}
		if err := tt.Click("New automation"); err == nil && m.Router().Path() != "/automations" {
			t.Fatal("old desktop opened an unsupported form")
		}
		m.State.AutomationCreate = true
		m.OpenAutomationCreate()
		v := mobileAutomationFixture()
		m.Autos.Form.Draft = &v
		m.Autos.Form.Request = "每天 18 点总结仓库变化"
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
	m.State.Automations = true
	v := mobileAutomationFixture()
	data, _ := json.Marshal([]automation.View{v})
	m.ReceiveAutomations(data, "")
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("Automations"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.Router().Path() != "/automations" || m.Menu || !tt.HasText("仓库日报") {
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
	if m.Open != 1 || m.Router().Path() != "/thread" {
		t.Fatal("run did not open its renumbered conversation")
	}
	openMobileDrawer(t, tt)
	if m.Router().Path() != "/thread" || m.Open != 1 || !m.Menu {
		t.Fatal("the conversation's arrow should open the drawer without leaving the run")
	}
	if err := tt.Click("Automations"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.Router().Path() != "/automations" || m.Open != 0 {
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
	m.Router().Push("/automations")
	tt := ui.NewTester(m.ViewRoot, 320, 568)
	tt.Frame()
	if !tt.HasText("Desktop update required") {
		t.Fatal("old desktop should explain that an update is needed")
	}
	m.State.Automations = true
	m.ReceiveAutomations(json.RawMessage(`[]`), "")
	tt.Frame()
	if !tt.HasText("No automations yet") {
		t.Fatal("empty automation state missing")
	}
	v := mobileAutomationFixture()
	data, _ := json.Marshal([]automation.View{v})
	m.ReceiveAutomations(data, "")
	m.ReceiveAutomations(nil, "Failed to load")
	m.ReceiveAutomations(json.RawMessage(`broken`), "")
	if len(m.Autos.List) != 1 || m.Autos.Loading || m.Autos.Err == "" {
		t.Fatal("a failed read lost the last successful list")
	}
	m.Connected = false
	tt.Frame()
	if !tt.HasText("Disconnected. Showing the last synced content.") || !tt.HasText(v.Name) {
		t.Fatal("offline state should retain the last read list")
	}
	m.Autos.Loading, m.Autos.RequestedAt = true, time.Now().Add(-11*time.Second)
	tt.Frame()
	if m.Autos.Loading || !tt.HasText("Read timed out. Try again.") {
		t.Fatal("a missing response should time out")
	}
	m.Forget()
	if len(m.Autos.List) != 0 {
		t.Fatal("unpairing retained another desktop's automations")
	}
}

func TestMobileAutomationRunDoesNotOpenReusedThreadID(t *testing.T) {
	m := testMobileApp()
	v := mobileAutomationFixture()
	r := v.Runs[0]
	r.Thread, r.ServerID = 1, "deleted"
	if _, ok := m.AutomationRunThread(v, r); ok {
		t.Fatal("a stale run opened a reused numeric thread ID")
	}
	r.ServerID = "s1"
	v.Project = "/b"
	if _, ok := m.AutomationRunThread(v, r); ok {
		t.Fatal("a run opened a conversation in another project")
	}
	v.Project, v.Agent = "/a", appmodel.AgentClaude
	if _, ok := m.AutomationRunThread(v, r); ok {
		t.Fatal("a run opened another agent's conversation")
	}
}

func TestMobileAutomationRunFeedbackAndCompatibility(t *testing.T) {
	m := testMobileApp()
	m.State.Automations = true
	v := mobileAutomationFixture()
	m.Autos.List, m.Autos.Loaded, m.Autos.Detail = []automation.View{v}, true, v.ID
	m.Router().Push("/automation")
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("Run now") || !tt.HasText("Update the desktop app to run now") {
		t.Fatal("old desktop did not explain manual run support")
	}
	if err := tt.Click("Run now"); err != nil {
		t.Fatal(err)
	}
	if m.Autos.RunRequest != "" {
		t.Fatal("unsupported desktop received a run request")
	}
	m.State.AutomationRun = true
	m.Autos.RunID, m.Autos.RunRequest, m.Autos.RunRequestedAt = v.ID, "request", time.Now()
	tt.Frame()
	if !tt.HasText("Starting…") || tt.HasText("Update the desktop app to run now") {
		t.Fatal("pending run feedback missing")
	}
	v.Running = true
	data, _ := json.Marshal(phoneapi.AutomationRunResult{RequestID: "stale", List: []automation.View{v}})
	m.ReceiveAutomationRun(data, "")
	if m.Autos.RunRequest != "request" || m.Autos.List[0].Running {
		t.Fatal("stale acknowledgement changed a pending request")
	}
	data, _ = json.Marshal(phoneapi.AutomationRunResult{RequestID: "request", List: []automation.View{v}})
	m.ReceiveAutomationRun(data, "")
	tt.Frame()
	if m.Autos.RunRequest != "" || !m.Autos.List[0].Running || !tt.HasText("Running") {
		t.Fatal("acknowledgement did not update the run status")
	}
	v.Running = false
	m.Autos.RunRequest = "failed"
	data, _ = json.Marshal(phoneapi.AutomationRunResult{RequestID: "failed", List: []automation.View{v}})
	m.ReceiveAutomationRun(data, "Project directory does not exist")
	tt.Frame()
	if !tt.HasText("Project directory does not exist") || m.Autos.RunRequest != "" {
		t.Fatal("run failure was not shown")
	}
	m.Autos.RunRequest, m.Autos.RunRequestedAt = "timeout", time.Now().Add(-31*time.Second)
	tt.Frame()
	if m.Autos.RunRequest != "" || !tt.HasText("Request timed out. Refresh to check run status.") {
		t.Fatal("run timeout did not release the pending request")
	}
	m.Autos.RunRequest = "disconnect"
	m.Disconnect()
	tt.Frame()
	if m.Autos.RunRequest != "" || !tt.HasText("Disconnected. Refresh to check run status.") {
		t.Fatal("disconnect left the run request pending")
	}
}

func TestMobileAutomationsDrawerLayout(t *testing.T) {
	m := mobileHomeFixture()
	tt := ui.NewTester(m.ViewRoot, 844, 390)
	tt.SetSafeArea(ui.SafeArea{Left: 59, Right: 59, Bottom: 21})
	tt.Frame()
	m.Menu = true
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
			m.State.Automations = true
			v := mobileAutomationFixture()
			v.Name = "每日检查仓库 Issue 和持续集成结果"
			v.Instructions = strings.Repeat("检查新 Issue 和测试结果，记录需要处理的问题。", 12)
			paused := v
			paused.ID, paused.Name, paused.Enabled = "paused", "仓库日报", false
			running := v
			running.ID, running.Name, running.Running = "running", "检查部署状态", true
			m.Autos.List, m.Autos.Loaded = []automation.View{v, paused, running}, true
			m.Router().Push("/automations")
			tt := ui.NewTester(m.ViewRoot, width, 844)
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
			if !m.Autos.InstructionsOpen || full.H <= preview.H {
				t.Fatal("task expansion did not reveal the full instructions")
			}
			if err := tt.Click("Collapse instructions"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
