package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"miya/internal/claude"
)

// Claude Code runs a sub-agent as a Task (lately Agent) tool call. It streams
// the sub-agent's own frames with parent_tool_use_id set to that call, and
// reports the sub-agent's state in system task frames.

// claudeSubAgent is the sub-agent a tool call starts, nil for other tools.
func claudeSubAgent(id, name string, input json.RawMessage, status string) []SubAgent {
	if name != "Task" && name != "Agent" {
		return nil
	}
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	return []SubAgent{{ID: id, Name: firstLine(inputString(in, "description", "prompt", "subagent_type")), Status: claudeSubAgentStatus(status)}}
}

func claudeSubAgentStatus(toolStatus string) string {
	switch toolStatus {
	case "completed":
		return subAgentCompleted
	case "failed":
		return subAgentFailed
	}
	return subAgentRunning
}

// claudeTaskStatus normalizes a task frame's status, "" when it tells none.
func claudeTaskStatus(s string) string {
	return claude.TaskStatus(s)
}

func applyClaudeAgentTasks(t *Thread, tasks []claude.AgentTask) {
	for _, task := range tasks {
		if a := claudeTaskAgent(t, task.ToolUseID, task.ID); a != nil {
			a.ID, a.Status = task.ID, task.Status
			if a.Name == "" {
				a.Name = firstLine(task.Name)
			}
			if task.Detail != "" {
				a.Detail = firstLine(task.Detail)
			}
			if task.Steps > a.Steps {
				a.Steps = task.Steps
			}
		}
	}
}

func interruptClaudeAgents(t *Thread, status string) {
	for i := range t.Messages {
		for j := range t.Messages[i].Agents {
			a := &t.Messages[i].Agents[j]
			if subAgentActive(a.Status) {
				a.Status, a.Detail = status, "进程已结束，未收到子任务完成通知"
			}
		}
	}
}

// claudeTaskFrame applies a system task frame, Claude Code's own report of a
// sub-agent's state, to the Task call that started the sub-agent.
func claudeTaskFrame(t *Thread, f claude.Frame) {
	a := claudeTaskAgent(t, f.ToolUseID, f.TaskID)
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
		a.Status = subAgentRunning
		if a.Name == "" {
			a.Name = firstLine(f.Description)
		}
	case "task_progress":
		if s := firstLine(f.Description); s != "" {
			a.Detail = s
		}
	case "task_updated":
		var patch struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(f.Patch, &patch)
		if s := claudeTaskStatus(patch.Status); s != "" {
			a.Status = s
		}
	case "task_notification":
		var status string
		_ = json.Unmarshal(f.Status, &status)
		if s := claudeTaskStatus(status); s != "" {
			a.Status = s
		}
		if s := firstLine(f.Summary); s != "" {
			a.Detail = s
		}
	}
}

// claudeTaskAgent finds a task frame's sub-agent by the call that started it,
// or by its task id for frames that carry only that.
func claudeTaskAgent(t *Thread, toolUseID, taskID string) *SubAgent {
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
func claudeSubAgentStep(t *Thread, parent string, blocks []claude.Block) {
	for i := range t.Messages {
		if t.Messages[i].ItemID != parent || len(t.Messages[i].Agents) == 0 {
			continue
		}
		a := &t.Messages[i].Agents[0]
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				a.Steps++
				a.Detail = claudeToolTitle(Message{Kind: "claudeTool", Status: "inProgress", Content: b.Name + "\n" + string(b.Input)})
			case "text":
				if s := firstLine(b.Text); s != "" {
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
func claudeFinishSubAgents(m *Message, output string, metadata json.RawMessage) {
	id, async := claude.AsyncAgentResult(metadata, output)
	if async && m.Status != "failed" {
		for i := range m.Agents {
			if id != "" {
				m.Agents[i].ID = id
			}
			// A repeated acknowledgement must not reopen a finished task.
			if subAgentActive(m.Agents[i].Status) {
				m.Agents[i].Detail = "已在后台启动，等待完成通知"
			}
		}
		return
	}
	if _, input := claudeToolCall(*m); input["run_in_background"] == true && m.Status != "failed" {
		return
	}
	report, steps := claudeTaskReport(output)
	for i := range m.Agents {
		m.Agents[i].Status = claudeSubAgentStatus(m.Status)
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
func claudeTaskReportText(output string) (string, int) {
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
func claudeTaskReport(output string) (string, int) {
	text, steps := claudeTaskReportText(output)
	return firstLine(text), steps
}

// claudeToolOutput is what a tool call shows of its output: a sub-agent's
// report without its wrapping, any other tool's output as it is.
func claudeToolOutput(m Message, output string) string {
	if len(m.Agents) > 0 {
		if _, async := claude.AsyncAgentResult(nil, output); async {
			return "子 agent 已在后台启动，等待完成通知。"
		}
		if text, _ := claudeTaskReportText(output); text != "" {
			return text
		}
	}
	return output
}
