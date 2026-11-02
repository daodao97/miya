package claude

import (
	"encoding/json"
	"encoding/xml"
	"regexp"
	"strings"
)

// AgentTask is a native Task/Agent invocation, independently of the tool's
// acknowledgement: an async acknowledgement does not complete the task.
type AgentTask struct {
	ID, ToolUseID, Name, Status, Detail string
	Steps                               int
	background                          bool
}

// AsyncAgentResult also recognizes older CLIs that only return launch text.
// New CLIs can choose async execution without run_in_background in the input.
func AsyncAgentResult(metadata json.RawMessage, output string) (id string, async bool) {
	var result struct {
		IsAsync bool   `json:"isAsync"`
		Status  string `json:"status"`
		AgentID string `json:"agentId"`
	}
	_ = json.Unmarshal(metadata, &result)
	async = result.IsAsync || result.Status == "async_launched" || strings.HasPrefix(strings.TrimSpace(output), "Async agent launched")
	if async {
		id = result.AgentID
		if id == "" {
			if m := asyncAgentID.FindStringSubmatch(output); m != nil {
				id = m[1]
			}
		}
	}
	return id, async
}

var asyncAgentID = regexp.MustCompile(`(?m)^agentId:\s*([a-zA-Z0-9_-]+)`)

// TaskNotification is how the native transcript records a background task's
// completion when it is delivered back to the parent as an internal message.
func TaskNotification(text string) (Frame, bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "<task-notification>") {
		return Frame{}, false
	}
	var n struct {
		XMLName   xml.Name `xml:"task-notification"`
		ID        string   `xml:"task-id"`
		ToolUseID string   `xml:"tool-use-id"`
		Status    string   `xml:"status"`
		Summary   string   `xml:"summary"`
	}
	if xml.Unmarshal([]byte(text), &n) != nil || n.ID == "" || TaskStatus(n.Status) == "" {
		return Frame{}, false
	}
	status, _ := json.Marshal(n.Status)
	return Frame{Type: "system", Subtype: "task_notification", TaskID: n.ID, ToolUseID: n.ToolUseID, Status: status, Summary: n.Summary}, true
}

func resultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []Block
	_ = json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TaskStatus(s string) string {
	switch strings.ToLower(s) {
	case "pending", "running", "started":
		return "running"
	case "completed", "success", "done":
		return "completed"
	case "failed", "error", "errored":
		return "failed"
	case "killed", "stopped", "cancelled", "canceled", "interrupted", "aborted":
		return "interrupted"
	}
	return ""
}

type agentTracker struct{ tasks []AgentTask }

func (s *agentTracker) task(toolID, taskID string) *AgentTask {
	for i := range s.tasks {
		a := &s.tasks[i]
		if toolID != "" && a.ToolUseID == toolID || taskID != "" && a.ID == taskID {
			if toolID != "" {
				a.ToolUseID = toolID
			}
			if taskID != "" {
				a.ID = taskID
			}
			return a
		}
	}
	if toolID == "" && taskID == "" {
		return nil
	}
	if taskID == "" {
		taskID = toolID
	}
	s.tasks = append(s.tasks, AgentTask{ID: taskID, ToolUseID: toolID, Status: "running"})
	return &s.tasks[len(s.tasks)-1]
}

func (s *agentTracker) active() bool {
	for _, a := range s.tasks {
		if a.Status == "running" {
			return true
		}
	}
	return false
}

func (s *agentTracker) apply(f *Frame) {
	if f.ParentToolUseID != "" {
		return
	}
	changed := false
	if f.Type == "assistant" {
		for _, b := range f.Message.Content {
			if b.Type != "tool_use" || b.Name != "Agent" && b.Name != "Task" {
				continue
			}
			var in struct {
				Description     string
				RunInBackground bool `json:"run_in_background"`
			}
			_ = json.Unmarshal(b.Input, &in)
			if a := s.task(b.ID, ""); a != nil {
				changed = true
				a.Name, a.background = in.Description, in.RunInBackground
			}
		}
	}
	if f.Type == "user" {
		for _, b := range f.Message.Content {
			if b.Type == "text" {
				if notification, ok := TaskNotification(b.Text); ok {
					s.apply(&notification)
					changed = true
				}
			}
			if b.Type != "tool_result" {
				continue
			}
			for i := range s.tasks {
				a := &s.tasks[i]
				if a.ToolUseID != b.ToolUseID {
					continue
				}
				changed = true
				if b.IsError {
					a.Status = "failed"
					continue
				}
				id, async := AsyncAgentResult(f.ToolUseResult, resultText(b.Content))
				if id != "" {
					a.ID = id
				}
				if !async && !a.background {
					a.Status = "completed"
				}
			}
		}
	}
	if f.Type == "system" && (f.Subtype == "task_started" || f.Subtype == "task_progress" || f.Subtype == "task_updated" || f.Subtype == "task_notification") {
		if a := s.task(f.ToolUseID, f.TaskID); a != nil {
			changed = true
			var usage struct {
				ToolUses int `json:"tool_uses"`
			}
			_ = json.Unmarshal(f.Usage, &usage)
			if usage.ToolUses > 0 {
				a.Steps = usage.ToolUses
			}
			switch f.Subtype {
			case "task_started":
				a.Status = "running"
				if a.Name == "" {
					a.Name = f.Description
				}
			case "task_progress":
				a.Detail = f.Description
			case "task_updated", "task_notification":
				var status string
				if f.Subtype == "task_updated" {
					var patch struct{ Status string }
					_ = json.Unmarshal(f.Patch, &patch)
					status = patch.Status
				} else {
					_ = json.Unmarshal(f.Status, &status)
				}
				if state := TaskStatus(status); state != "" {
					a.Status = state
				}
				if f.Summary != "" {
					a.Detail = f.Summary
				}
			}
		}
	}
	if len(s.tasks) > 0 {
		if changed {
			f.AgentTasks = append([]AgentTask(nil), s.tasks...)
		}
		f.WaitingForAgents = s.active()
	}
}
