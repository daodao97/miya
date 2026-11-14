package integration

import (
	"miya/internal/appmodel"
	"miya/internal/automation"
	"testing"
)

func mobileAutomationFixture() automation.View {
	return automation.ViewOf(automation.Automation{ID: "daily", Name: "仓库日报", Project: "/a", Agent: appmodel.AgentCodex, Instructions: "总结仓库的最新变化。", Schedule: "0 18 * * *", Enabled: true}, []automation.Run{{Started: atLocal("2026-11-01 18:00"), Thread: 99, ServerID: "s1", Result: automation.Passed, Summary: "日报已完成", Report: &automation.Report{Summary: "已完成检查", Findings: []string{"测试全部通过"}}}}, false, atLocal("2026-11-01 19:00"))
}

func TestMobileAutomationRunDoesNotOpenReusedThreadID(t *testing.T) {
	m := testPhoneClient()
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
