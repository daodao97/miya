package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Each automation run ends with a structured result: the agent reports it
// with the miya report_result tool, or Miya makes one from the
// conversation's last reply. It is kept with the run, so the next run can
// compare against it after the conversation itself is gone.

// RunReport is a run's result.
type RunReport struct {
	Summary   string      `json:"summary"`
	Findings  []string    `json:"findings,omitempty"`
	Metrics   []RunMetric `json:"metrics,omitempty"`
	Artifacts []string    `json:"artifacts,omitempty"`
	NeedsUser bool        `json:"needs_user,omitempty"`
	// Auto marks a result Miya made because the agent reported none.
	Auto bool `json:"auto,omitempty"`
}

// RunMetric is one measured value of a run, such as an amount or a share.
type RunMetric struct {
	Name  string     `json:"name"`
	Value flexString `json:"value"`
	Unit  string     `json:"unit,omitempty"`
}

// flexString takes a JSON string or number: agents send either.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = flexString(strings.TrimSpace(string(b)))
	return nil
}

func (m RunMetric) text() string {
	return strings.TrimSpace(m.Name) + "：" + strings.TrimSpace(string(m.Value)) + strings.TrimSpace(m.Unit)
}

const (
	reportSummaryLimit = 2000
	reportItemLimit    = 500
	reportListLimit    = 30
)

// cleaned bounds a reported result.
func (r RunReport) cleaned() RunReport {
	list := func(items []string) []string {
		var out []string
		for _, s := range items {
			if s = strings.TrimSpace(s); s != "" && len(out) < reportListLimit {
				out = append(out, clipRunes(s, reportItemLimit))
			}
		}
		return out
	}
	out := RunReport{Summary: clipRunes(strings.TrimSpace(r.Summary), reportSummaryLimit), Findings: list(r.Findings), Artifacts: list(r.Artifacts), NeedsUser: r.NeedsUser, Auto: r.Auto}
	for _, m := range r.Metrics {
		if strings.TrimSpace(m.Name) != "" && len(out.Metrics) < reportListLimit {
			out.Metrics = append(out.Metrics, RunMetric{Name: clipRunes(strings.TrimSpace(m.Name), 100), Value: flexString(clipRunes(strings.TrimSpace(string(m.Value)), 100)), Unit: clipRunes(strings.TrimSpace(m.Unit), 20)})
		}
	}
	return out
}

// autoRunReport is the result of a run whose agent reported none: its last
// reply and the files it changed.
func autoRunReport(t Thread) *RunReport {
	reply := lastReply(t.Messages)
	files := changedFiles(t.Messages)
	if reply == "" && len(files) == 0 {
		return nil
	}
	r := RunReport{Summary: reply, Artifacts: files, Auto: true}.cleaned()
	return &r
}

// lastReport is the result of an automation's latest run that has one.
func (s automationStore) lastReport(id string) (AutomationRun, bool) {
	runs := s.Runs[id]
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Report != nil && runs[i].Result != runRunning {
			return runs[i], true
		}
	}
	return AutomationRun{}, false
}

// formatRunReport writes a result as a message reads it.
func formatRunReport(r RunReport) string {
	var b strings.Builder
	b.WriteString("总结：" + r.Summary + "\n")
	if len(r.Findings) > 0 {
		b.WriteString("要点：\n")
		for _, f := range r.Findings {
			b.WriteString("- " + f + "\n")
		}
	}
	if len(r.Metrics) > 0 {
		b.WriteString("指标：\n")
		for _, m := range r.Metrics {
			b.WriteString("- " + m.text() + "\n")
		}
	}
	if len(r.Artifacts) > 0 {
		b.WriteString("产出：" + strings.Join(r.Artifacts, "、") + "\n")
	}
	if r.NeedsUser {
		b.WriteString("当时需要人处理。\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// automationRunMessage is what a run sends: the instructions, how to report
// the result, and the previous result to compare with.
func automationRunMessage(a Automation, prev *AutomationRun) string {
	var b strings.Builder
	b.WriteString(a.Instructions)
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "这是 Miya 的定时自动化「%s」。结束前请调用 miya 的 report_result 工具上报本次结果：summary（一两句总结）、findings（结论要点）、metrics（关键指标，每项含 name、value、unit）、artifacts（产出的文件或链接）、needs_user（是否需要人处理）。", a.Name)
	if prev != nil && prev.Report != nil {
		fmt.Fprintf(&b, "\n\n上一次运行（%s）的结果如下，请与本次结果对比，指出新增、消失和变化较大的项：\n%s", formatAutomationTime(prev.Started), formatRunReport(*prev.Report))
	}
	return b.String()
}

func init() {
	mcpTools = append(mcpTools, mcpTool{Name: "report_result",
		Description: "In a Miya automation run, report the run's structured result before finishing: a short summary, key findings, metrics, files or links produced, and whether the user needs to act. " +
			"A later call replaces an earlier one. Only automation runs use it.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"summary":  map[string]any{"type": "string", "description": "One or two sentences on what this run found or did"},
			"findings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Key findings, one per item"},
			"metrics": map[string]any{"type": "array", "description": "Measured values to compare across runs", "items": map[string]any{"type": "object", "properties": map[string]any{
				"name":  map[string]any{"type": "string"},
				"value": map[string]any{"type": []string{"string", "number"}},
				"unit":  map[string]any{"type": "string"},
			}, "required": []string{"name", "value"}}},
			"artifacts":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Files or links produced"},
			"needs_user": map[string]any{"type": "boolean", "description": "Whether the user needs to act on this result"},
		}, "required": []string{"summary"}},
		Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}})
}

// mcpReportResult keeps a reported result with the caller's run.
func (c *chatController) mcpReportResult(r mcpRequest) (any, error) {
	if c.auto == nil {
		return nil, errors.New("自动化需要 Miya 后台服务")
	}
	if r.Report == nil || strings.TrimSpace(r.Report.Summary) == "" {
		return nil, errors.New("summary 不能为空")
	}
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpCallerUnknown)
	}
	for id, run := range c.auto.active {
		if run.thread != caller.ID {
			continue
		}
		rep := r.Report.cleaned()
		rep.Auto = false
		c.auto.store.updateLastRun(id, func(x *AutomationRun) { x.Report = &rep })
		c.auto.persist()
		return "已记录本次运行的结果。", nil
	}
	return nil, errors.New("这个对话不是正在运行的自动化，不需要上报结果")
}
