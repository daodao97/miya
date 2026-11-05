package transcript

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"miya/internal/claude"
	"miya/internal/model"
	"miya/internal/subagent"
	"miya/internal/textutil"
)

// claudeSubAgent is the sub-agent a tool call starts, nil for other tools.
func SubAgents(id, name string, input json.RawMessage, status string) []model.SubAgent {
	if name != "Task" && name != "Agent" {
		return nil
	}
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	return []model.SubAgent{{ID: id, Name: textutil.FirstLine(InputString(in, "description", "prompt", "subagent_type")), Status: SubAgentStatus(status)}}
}

func SubAgentStatus(toolStatus string) string {
	switch toolStatus {
	case "completed":
		return subagent.Completed
	case "failed":
		return subagent.Failed
	}
	return subagent.Running
}

// claudeTaskStatus normalizes a task frame's status, "" when it tells none.
func TaskStatus(s string) string {
	return claude.TaskStatus(s)
}

func ApplyAgentTasks(t *model.Thread, tasks []claude.AgentTask) {
	for _, task := range tasks {
		if a := TaskAgent(t, task.ToolUseID, task.ID); a != nil {
			a.ID, a.Status = task.ID, task.Status
			if a.Name == "" {
				a.Name = textutil.FirstLine(task.Name)
			}
			if task.Detail != "" {
				a.Detail = textutil.FirstLine(task.Detail)
			}
			if task.Steps > a.Steps {
				a.Steps = task.Steps
			}
		}
	}
}

func InterruptAgents(t *model.Thread, status string) {
	for i := range t.Messages {
		for j := range t.Messages[i].Agents {
			a := &t.Messages[i].Agents[j]
			if subagent.Active(a.Status) {
				a.Status, a.Detail = status, "Process exited without a subtask completion notification"
			}
		}
	}
}

// claudeTaskFrame applies a system task frame, Claude Code's own report of a
// sub-agent's state, to the Task call that started the sub-agent.
func TaskFrame(t *model.Thread, f claude.Frame) {
	if f.BackgroundWork || f.TaskType != "" && f.TaskType != "local_agent" && f.TaskType != "remote_agent" {
		return
	}
	a := TaskAgent(t, f.ToolUseID, f.TaskID)
	if a == nil {
		return
	}
	if f.TaskID != "" {
		a.ID = f.TaskID
	}
	var usage struct {
		ToolUses int `json:"tool_uses"`
	}
	_ = json.Unmarshal(f.Usage, &usage)
	if usage.ToolUses > 0 {
		a.Steps = usage.ToolUses
	}
	switch f.Subtype {
	case "task_started":
		a.Status = subagent.Running
		if a.Name == "" {
			a.Name = textutil.FirstLine(f.Description)
		}
	case "task_progress":
		if s := textutil.FirstLine(f.Description); s != "" {
			a.Detail = s
		}
	case "task_updated":
		var patch struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(f.Patch, &patch)
		if s := TaskStatus(patch.Status); s != "" {
			a.Status = s
		}
	case "task_notification":
		var status string
		_ = json.Unmarshal(f.Status, &status)
		if s := TaskStatus(status); s != "" {
			a.Status = s
		}
		if s := textutil.FirstLine(f.Summary); s != "" {
			a.Detail = s
		}
	}
}

// claudeTaskAgent finds a task frame's sub-agent by the call that started it,
// or by its task id for frames that carry only that.
func TaskAgent(t *model.Thread, toolUseID, taskID string) *model.SubAgent {
	for i := range t.Messages {
		m := &t.Messages[i]
		if len(m.Agents) == 0 {
			continue
		}
		if (toolUseID != "" && m.ItemID == toolUseID) || (taskID != "" && m.Agents[0].ID == taskID) {
			return &m.Agents[0]
		}
	}
	return nil
}

// claudeSubAgentStep records what a sub-agent sent on its Task call: the
// tool it calls, or what it says, becomes its latest step.
func SubAgentStep(t *model.Thread, parent string, blocks []claude.Block) {
	for i := range t.Messages {
		if t.Messages[i].ItemID != parent || len(t.Messages[i].Agents) == 0 {
			continue
		}
		a := &t.Messages[i].Agents[0]
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				a.Steps++
				a.Detail = ToolTitle(model.Message{Kind: "claudeTool", Status: "inProgress", Content: b.Name + "\n" + string(b.Input)})
			case "text":
				if s := textutil.FirstLine(b.Text); s != "" {
					a.Detail = s
				}
			}
		}
		return
	}
}

// claudeFinishSubAgents settles a Task call's sub-agent as its result
// arrives, the result's first line its last word. A background sub-agent's
// call returns as it starts; its task frames tell when it ends.
func FinishSubAgents(m *model.Message, output string, metadata json.RawMessage) {
	id, async := claude.AsyncAgentResult(metadata, output)
	if async && m.Status != "failed" {
		for i := range m.Agents {
			if id != "" {
				m.Agents[i].ID = id
			}
			// A repeated acknowledgement must not reopen a finished task.
			if subagent.Active(m.Agents[i].Status) {
				m.Agents[i].Detail = "Started in the background; waiting for completion"
			}
		}
		return
	}
	if _, input := ToolCall(*m); input["run_in_background"] == true && m.Status != "failed" {
		return
	}
	report, steps := TaskReport(output)
	for i := range m.Agents {
		m.Agents[i].Status = SubAgentStatus(m.Status)
		if report != "" {
			m.Agents[i].Detail = report
		}
		if steps > 0 {
			m.Agents[i].Steps = steps
		}
	}
}

var (
	claudeResultTag   = regexp.MustCompile(`(?s)<([a-z][a-z0-9_-]*)>(.*?)</[a-z][a-z0-9_-]*>`)
	claudeResultSteps = regexp.MustCompile(`tool_uses:\s*(\d+)`)
)

// claudeTaskReportText reads a Task call's result as Claude Code wraps it:
// tags such as <usage>, a hand-back frame ending in "The report follows:",
// the sub-agent's report indented by two spaces, then an agentId line. It
// returns the report and the tools the sub-agent used, as <usage> counts
// them; a result without the wrapping is its own report.
func TaskReportText(output string) (string, int) {
	steps := 0
	for _, tag := range claudeResultTag.FindAllStringSubmatch(output, -1) {
		if tag[1] == "usage" {
			if n := claudeResultSteps.FindStringSubmatch(tag[2]); n != nil {
				steps, _ = strconv.Atoi(n[1])
			}
		}
	}
	text := claudeResultTag.ReplaceAllString(output, "")
	wrapped := false
	if _, report, ok := strings.Cut(text, "The report follows:"); ok {
		text, wrapped = report, true
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "agentId:") {
			continue
		}
		if wrapped {
			line = strings.TrimPrefix(line, "  ")
		}
		lines = append(lines, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), steps
}

// claudeTaskReport is the first line of a Task call's report, and the tools
// its sub-agent used.
func TaskReport(output string) (string, int) {
	text, steps := TaskReportText(output)
	return textutil.FirstLine(text), steps
}

// claudeToolOutput is what a tool call shows of its output: a sub-agent's
// report without its wrapping, any other tool's output as it is.
func ToolOutput(m model.Message, output string) string {
	if len(m.Agents) > 0 {
		if _, async := claude.AsyncAgentResult(nil, output); async {
			return "Subagent started in the background. Waiting for completion."
		}
		if text, _ := TaskReportText(output); text != "" {
			return text
		}
	}
	return output
}
