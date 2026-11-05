package main

import (
	"errors"
	"fmt"
	"miya/internal/automation"
	"strings"
)

// autoRunReport is the result of a run whose agent reported none: its last
// reply and the files it changed.
func autoRunReport(t Thread) *automation.Report {
	reply := lastReply(t.Messages)
	files := changedFiles(t.Messages)
	if reply == "" && len(files) == 0 {
		return nil
	}
	r := automation.Report{Summary: reply, Artifacts: files, Auto: true}.Cleaned()
	return &r
}

// automationRunMessage is what a run sends: the instructions, how to report
// the result, and the previous result to compare with.
func automationRunMessage(a automation.Automation, prev *automation.Run) string {
	var b strings.Builder
	b.WriteString(a.Instructions)
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "This is Miya scheduled automation \"%s\". Before finishing, call Miya's report_result tool with this run's result: summary (one or two sentences), findings (key conclusions), metrics (each with name, value, unit), artifacts (output files or links), and needs_user (whether user action is required).", a.Name)
	if prev != nil && prev.Report != nil {
		fmt.Fprintf(&b, "\n\nResults from the previous run (%s) follow. Compare them with this run and identify new, removed, and significantly changed items:\n%s", formatAutomationTime(prev.Started), automation.FormatReport(*prev.Report))
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
		return nil, errors.New("Automations require the Miya background service")
	}
	if r.Report == nil || strings.TrimSpace(r.Report.Summary) == "" {
		return nil, errors.New("Summary is required")
	}
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpCallerUnknown)
	}
	for id, run := range c.auto.active {
		if run.thread != caller.ID {
			continue
		}
		rep := r.Report.Cleaned()
		rep.Auto = false
		c.auto.store.UpdateLastRun(id, func(x *automation.Run) { x.Report = &rep })
		c.auto.persist()
		return "Run result recorded.", nil
	}
	return nil, errors.New("This conversation is not an active automation run. No report is needed.")
}
