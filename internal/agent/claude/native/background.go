package claude

import (
	"encoding/json"
	"sort"
)

// BackgroundTask describes native work, not an OS process. ID is opaque.
type BackgroundTask struct {
	ID, RunID, ToolUseID, Kind, Description, Command, Status string
}
type backgroundTracker struct {
	tasks   map[string]BackgroundTask
	tools   map[string]BackgroundTask
	known   bool
	retired map[string]string
}

func (s *backgroundTracker) active() bool {
	for _, t := range s.tasks {
		if t.Status == "running" || t.Status == "pending" {
			return true
		}
	}
	return false
}
func (s *backgroundTracker) snapshot() []BackgroundTask {
	out := make([]BackgroundTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *backgroundTracker) apply(f *Frame) {
	defer func() { f.BackgroundTasks = s.snapshot() }()
	if s.tasks == nil {
		s.tasks = map[string]BackgroundTask{}
		s.tools = map[string]BackgroundTask{}
		s.retired = map[string]string{}
	}
	if f.Type == "assistant" {
		for _, b := range f.Message.Content {
			if b.Type != "tool_use" || b.Name != "Bash" {
				continue
			}
			var in struct {
				Command, Description string
				Background           bool `json:"run_in_background"`
			}
			_ = json.Unmarshal(b.Input, &in)
			s.tools[b.ID] = BackgroundTask{ToolUseID: b.ID, Kind: "local_bash", Description: in.Description, Command: in.Command, Status: "pending"}
		}
	}
	if f.Type == "user" {
		for _, b := range f.Message.Content {
			if b.Type == "text" {
				if notification, ok := TaskNotification(b.Text); ok {
					s.apply(&notification)
				}
			}
			if b.Type != "tool_result" {
				continue
			}
			tool, ok := s.tools[b.ToolUseID]
			if !ok {
				continue
			}
			var result struct {
				BackgroundTaskID string `json:"backgroundTaskId"`
			}
			_ = json.Unmarshal(f.ToolUseResult, &result)
			if result.BackgroundTaskID != "" && !b.IsError {
				if _, retired := s.retired[result.BackgroundTaskID]; retired {
					continue
				}
				tool.ID = result.BackgroundTaskID
				tool.Status = "running"
				s.tasks[tool.ID] = tool
			}
		}
	}
	if f.Type != "system" {
		return
	}
	if f.Subtype == "background_tasks_changed" {
		if !validBackgroundSnapshot(f) {
			return
		}
		next := map[string]BackgroundTask{}
		for _, entry := range f.Tasks {
			// Only shell/monitor work belongs in this tracker; agentTracker retains
			// sub-agent presentation and scheduleTracker retains session schedules.
			if entry.TaskType == "local_agent" || entry.TaskType == "remote_agent" {
				continue
			}
			t := s.tasks[entry.TaskID]
			if retired, ok := s.retired[entry.TaskID]; ok && (entry.RunID == "" || entry.RunID <= retired) {
				continue
			}
			if t.RunID != "" && entry.RunID != "" && entry.RunID < t.RunID {
				continue
			}
			t.ID, t.RunID, t.Kind, t.Description, t.Status = entry.TaskID, entry.RunID, entry.TaskType, entry.Description, "running"
			next[t.ID] = t
		}
		for id, t := range s.tasks {
			if _, ok := next[id]; !ok {
				s.retired[id] = t.RunID
			}
		}
		s.tasks = next
		s.known = true
		return
	}
	if f.Subtype != "task_started" && f.Subtype != "task_progress" && f.Subtype != "task_updated" && f.Subtype != "task_notification" {
		return
	}
	t, exists := s.tasks[f.TaskID]
	tool, isBash := s.tools[f.ToolUseID]
	if !exists && !isBash && (f.TaskType == "" || f.TaskType == "local_agent" || f.TaskType == "remote_agent") {
		return
	}
	f.BackgroundWork = true
	if f.TaskID == "" {
		return
	}
	if old, ok := s.retired[f.TaskID]; ok && f.Subtype == "task_started" {
		if f.RunID == "" || f.RunID <= old {
			return
		}
		delete(s.retired, f.TaskID)
	}
	if exists && t.RunID != "" && f.RunID != "" && f.RunID < t.RunID {
		return
	}
	if !exists {
		t = tool
		t.ID = f.TaskID
		t.Kind = f.TaskType
		if t.Kind == "" {
			t.Kind = "local_bash"
		}
	}
	if f.RunID != "" {
		t.RunID = f.RunID
	}
	if f.ToolUseID != "" {
		t.ToolUseID = f.ToolUseID
	}
	if f.Description != "" {
		t.Description = f.Description
	}
	switch f.Subtype {
	case "task_started":
		if t.Status == "" || t.Status == "pending" || t.RunID != s.tasks[f.TaskID].RunID {
			t.Status = "running"
		}
	case "task_updated":
		var patch struct{ Status, Description string }
		_ = json.Unmarshal(f.Patch, &patch)
		if state := TaskStatus(patch.Status); state != "" {
			t.Status = state
		}
		if patch.Description != "" {
			t.Description = patch.Description
		}
	case "task_notification":
		var status string
		_ = json.Unmarshal(f.Status, &status)
		if state := TaskStatus(status); state != "" {
			t.Status = state
		}
	}
	if t.Status == "completed" || t.Status == "failed" || t.Status == "interrupted" {
		s.retired[t.ID] = t.RunID
	}
	s.tasks[t.ID] = t
}

// Runtime returns immutable native task data and this CLI's dedicated OS root.
// A task ID is never converted to an OS PID.
func (c *Client) Runtime() (tasks []BackgroundTask, known bool, pid int, epoch string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]BackgroundTask(nil), c.background...), c.backgroundKnown, c.pid, c.epoch
}

func (c *Client) Done() <-chan struct{} { return c.done }

func validBackgroundSnapshot(f *Frame) bool {
	if f.Tasks == nil {
		return false
	}
	seen := map[string]bool{}
	for _, task := range f.Tasks {
		if task.TaskID == "" || task.TaskType == "" || seen[task.TaskID] {
			return false
		}
		seen[task.TaskID] = true
	}
	return true
}

func (c *Client) closeIdleInput() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idle && !c.open && !c.stopping {
		c.idle = false
		_ = c.in.Close()
	}
}
