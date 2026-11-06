package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	automation "miya/internal/automation"
	strings "strings"
	testing "testing"
	time "time"
)

func reportCall(h *automationHarness, thread int) {
	h.c.Update(thread, func(th *appmodel.Thread) {
		th.Messages = append(th.Messages, appmodel.Message{ID: h.c.NextID(), Role: "tool", Kind: "claudeTool", Status: "inProgress", Content: "mcp__miya__report_result\n{}"})
	})
}

func TestAutomationRunsReportAndCarryResults(t *testing.T) {
	h := newAutomationHarness(t)
	a := h.add(t, automation.Automation{Name: "上游对账", Instructions: "对账", Schedule: "* * * * *"})
	h.now = h.now.Add(time.Minute)
	h.c.Auto.Tick()
	if !strings.HasPrefix(h.sent[0].Text, "对账") || !strings.Contains(h.sent[0].Text, "report_result") || strings.Contains(h.sent[0].Text, "previous run") {
		t.Fatalf("bad first message: %q", h.sent[0].Text)
	}
	thread := h.last(a.ID).Thread
	reportCall(h, thread)
	var rep automation.Report
	if err := json.Unmarshal([]byte(`{"summary":"上游多扣 3 笔\n细节","findings":["pomo 多扣 12.3 元"],"metrics":[{"name":"多扣占比","value":0.8,"unit":"%"}],"needs_user":true}`), &rep); err != nil {
		t.Fatal(err)
	}
	res, err := h.c.McpHandle("mcpReportResult", appmodel.McpRequest{Tool: "report_result", Report: &rep})
	if err != nil || !strings.Contains(res.(string), "recorded") {
		t.Fatalf("report not kept: %v %v", res, err)
	}
	h.c.Update(thread, func(th *appmodel.Thread) { th.Messages[len(th.Messages)-1].Status = "completed" })
	h.endTurn(thread, "Done")
	h.c.Auto.Tick()
	r := h.last(a.ID)
	if r.Result != automation.Passed || r.Report == nil || r.Report.Auto || !r.Report.NeedsUser || r.Summary != "上游多扣 3 笔" || string(r.Report.Metrics[0].Value) != "0.8" {
		t.Fatalf("bad finished run: %+v %+v", r, r.Report)
	}

	h.now = h.now.Add(time.Minute)
	h.c.Auto.Tick()
	if len(h.sent) != 2 || !strings.Contains(h.sent[1].Text, "previous run") || !strings.Contains(h.sent[1].Text, "多扣占比: 0.8%") || !strings.Contains(h.sent[1].Text, "pomo 多扣 12.3 元") {
		t.Fatalf("previous result not carried: %q", h.sent[len(h.sent)-1].Text)
	}

	// Without a report, the run keeps an automatic summary of its reply.
	thread2 := h.last(a.ID).Thread
	h.endTurn(thread2, "这次没有异常")
	h.c.Auto.Tick()
	if r := h.last(a.ID); r.Report == nil || !r.Report.Auto || r.Report.Summary != "这次没有异常" || r.Summary != "这次没有异常" {
		t.Fatalf("no automatic report: %+v %+v", r, r.Report)
	}

	// Turned off, the next run goes without it.
	h.s.Store.Automations[0].NoCarryOver = true
	h.now = h.now.Add(time.Minute)
	h.c.Auto.Tick()
	if strings.Contains(h.sent[len(h.sent)-1].Text, "previous run") {
		t.Fatal("previous result carried while off")
	}
}

func TestReportResultOutsideAutomations(t *testing.T) {
	h := newAutomationHarness(t)
	h.c.Run = func(f func()) { f() }
	automationCaller(h, "report_result")
	if _, err := h.c.McpHandle("mcpReportResult", appmodel.McpRequest{Tool: "report_result", Report: &automation.Report{Summary: "x"}}); err == nil || !strings.Contains(err.Error(), "not an active automation") {
		t.Fatalf("report outside a run accepted: %v", err)
	}
	if _, err := h.c.McpHandle("mcpReportResult", appmodel.McpRequest{Tool: "report_result", Report: &automation.Report{}}); err == nil {
		t.Fatal("empty summary accepted")
	}
	long := automation.Report{Summary: strings.Repeat("长", 3000), Findings: make([]string, 50)}
	for i := range long.Findings {
		long.Findings[i] = "f"
	}
	if c := long.Cleaned(); len([]rune(c.Summary)) > automation.ReportSummaryLimit+1 || len(c.Findings) != automation.ReportListLimit {
		t.Fatal("report not bounded")
	}
}
