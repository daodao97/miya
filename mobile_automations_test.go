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

	"github.com/egoist/mygo/ui"
)

func mobileAutomationFixture() automationView {
	return automationViewOf(Automation{ID: "daily", Name: "仓库日报", Project: "/a", Agent: agentCodex, Instructions: "总结仓库的最新变化。", Schedule: "0 18 * * *", Enabled: true}, []AutomationRun{{Started: atLocal("2026-11-01 18:00"), Thread: 99, ServerID: "s1", Result: runPassed, Summary: "日报已完成", Report: &RunReport{Summary: "已完成检查", Findings: []string{"测试全部通过"}}}}, false, atLocal("2026-11-01 19:00"))
}

func TestMobileAutomationsNavigationAndRun(t *testing.T) {
	m := testMobileApp()
	m.state.Automations = true
	v := mobileAutomationFixture()
	data, _ := json.Marshal([]automationView{v})
	m.receiveAutomations(data, "")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("自动化"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.router().Path() != "/automations" || m.menu || !tt.HasText("仓库日报") {
		t.Fatal("drawer did not open the automation list")
	}
	if err := tt.Click("仓库日报"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"自动化详情", "总结仓库的最新变化。", "运行历史", "日报已完成", "打开对话"} {
		if !tt.HasText(text) {
			t.Fatalf("detail missing %q: %v", text, tt.Texts())
		}
	}
	if tt.HasText("测试全部通过") {
		t.Fatal("full results should stay collapsed until requested")
	}
	if err := tt.Click("查看结果"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("测试全部通过") || !tt.HasText("已完成检查") {
		t.Fatal("expanding the result lost the structured report")
	}
	automationSnapshot(t, tt, "mobile-automation-expanded")
	if err := tt.Click("收起结果"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("打开运行对话"); err != nil {
		t.Fatal(err)
	}
	if m.open != 1 || m.router().Path() != "/thread" {
		t.Fatal("run did not open its renumbered conversation")
	}
	openMobileDrawer(t, tt)
	if m.router().Path() != "/thread" || m.open != 1 || !m.menu {
		t.Fatal("the conversation's arrow should open the drawer without leaving the run")
	}
	if err := tt.Click("自动化"); err != nil {
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
	if !tt.HasText("需要更新桌面端") {
		t.Fatal("old desktop should explain that an update is needed")
	}
	m.state.Automations = true
	m.receiveAutomations(json.RawMessage(`[]`), "")
	tt.Frame()
	if !tt.HasText("还没有自动化") {
		t.Fatal("empty automation state missing")
	}
	v := mobileAutomationFixture()
	data, _ := json.Marshal([]automationView{v})
	m.receiveAutomations(data, "")
	m.receiveAutomations(nil, "读取失败")
	m.receiveAutomations(json.RawMessage(`broken`), "")
	if len(m.autos.list) != 1 || m.autos.loading || m.autos.err == "" {
		t.Fatal("a failed read lost the last successful list")
	}
	m.connected = false
	tt.Frame()
	if !tt.HasText("连接已断开，以下为上次同步的内容。") || !tt.HasText(v.Name) {
		t.Fatal("offline state should retain the last read list")
	}
	m.autos.loading, m.autos.requestedAt = true, time.Now().Add(-11*time.Second)
	tt.Frame()
	if m.autos.loading || !tt.HasText("读取超时，请重试") {
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
	if _, ok := tt.Find("自动化"); !ok {
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
			m.autos.list, m.autos.loaded = []automationView{v, paused, running}, true
			m.router().Push("/automations")
			tt := ui.NewTester(m.viewRoot, width, 844)
			tt.SetDark(dark)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			tt.Frame()
			name := fmt.Sprintf("mobile-automation-list-%d-dark-%v", width, dark)
			automationSnapshot(t, tt, name)
			tt.SetPreferences(ui.Preferences{TextScale: 1.6})
			for _, label := range []string{v.Name, "打开侧边栏", "刷新自动化"} {
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
			if err := tt.Click("展开任务内容"); err != nil {
				t.Fatal(err)
			}
			full, _ := tt.Find(v.Instructions)
			if !m.autos.instructionsOpen || full.H <= preview.H {
				t.Fatal("task expansion did not reveal the full instructions")
			}
			if err := tt.Click("收起任务内容"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
