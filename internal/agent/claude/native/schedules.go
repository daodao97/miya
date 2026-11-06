package claude

import (
	"encoding/json"
	"slices"
	"time"
)

// ScheduledTask uses the native tool_use_result metadata, including Claude's
// actual next fire time (which may include scheduler jitter).
type ScheduledTask struct {
	ID, Prompt, Cron, Status, Detail string
	NextFireAt                       int64
	Recurring                        bool
	CreatedAt                        int64
	UnrescheduledWakeups             int
}

type scheduleCall struct {
	name  string
	input json.RawMessage
}

type scheduleTracker struct {
	calls map[string]scheduleCall
	tasks []ScheduledTask
	known bool
}

func (s *scheduleTracker) active() bool {
	return slices.ContainsFunc(s.tasks, func(t ScheduledTask) bool { return t.Status == "waiting" || t.Status == "running" })
}

func (s *scheduleTracker) set(t ScheduledTask) {
	s.known = true
	for i := range s.tasks {
		if s.tasks[i].ID == t.ID {
			if t.CreatedAt == 0 {
				t.CreatedAt = s.tasks[i].CreatedAt
			}
			s.tasks[i] = t
			return
		}
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().UnixMilli()
	}
	s.tasks = append(s.tasks, t)
}

func (s *scheduleTracker) apply(f *Frame) {
	if f.ParentToolUseID != "" {
		return
	}
	if f.Type == "assistant" {
		for _, b := range f.Message.Content {
			if b.Type == "tool_use" && (b.Name == "CronCreate" || b.Name == "CronDelete" || b.Name == "CronList" || b.Name == "ScheduleWakeup") {
				if s.calls == nil {
					s.calls = map[string]scheduleCall{}
				}
				s.calls[b.ID] = scheduleCall{b.Name, b.Input}
			}
		}
	}
	if f.Type == "user" {
		for _, b := range f.Message.Content {
			if b.Type != "tool_result" {
				continue
			}
			call, ok := s.calls[b.ToolUseID]
			delete(s.calls, b.ToolUseID)
			if !ok || b.IsError {
				continue
			}
			var in struct {
				ID, Prompt, Cron, Reason string
				Stop                     bool
			}
			_ = json.Unmarshal(call.input, &in)
			var result struct {
				ID           string
				NextFireAt   float64
				ScheduledFor float64
				Recurring    bool
			}
			_ = json.Unmarshal(f.ToolUseResult, &result)
			switch call.name {
			case "CronCreate":
				if result.ID != "" {
					s.set(ScheduledTask{ID: result.ID, Prompt: in.Prompt, Cron: in.Cron, Status: "waiting", NextFireAt: int64(result.NextFireAt), Recurring: result.Recurring})
				}
			case "CronDelete":
				for i := range s.tasks {
					if s.tasks[i].ID == in.ID {
						s.tasks[i].Status, s.tasks[i].NextFireAt = "stopped", 0
					}
				}
			case "ScheduleWakeup":
				if in.Stop {
					for i := range s.tasks {
						if s.tasks[i].ID == "wakeup" {
							s.tasks[i].Status, s.tasks[i].NextFireAt = "completed", 0
						}
					}
				} else if result.ScheduledFor > 0 {
					s.set(ScheduledTask{ID: "wakeup", Prompt: in.Prompt, Detail: in.Reason, Status: "waiting", NextFireAt: int64(result.ScheduledFor), Recurring: true})
				}
			case "CronList":
				s.list(f.ToolUseResult)
			}
		}
	}
	if f.Type == "command_lifecycle" && f.CommandState == "started" && f.WakeupSource == "schedule_wakeup" {
		now := time.Now().UnixMilli()
		active := 0
		for _, task := range s.tasks {
			if task.Status == "waiting" || task.Status == "running" {
				active++
			}
		}
		for i := range s.tasks {
			if s.tasks[i].Status == "waiting" && (s.tasks[i].NextFireAt > 0 && s.tasks[i].NextFireAt <= now || active == 1 && s.tasks[i].NextFireAt == 0) {
				s.tasks[i].Status, s.tasks[i].NextFireAt = "running", 0
				if s.tasks[i].CreatedAt > 0 && now-s.tasks[i].CreatedAt >= int64(7*24*time.Hour/time.Millisecond) {
					s.tasks[i].Recurring = false
				}
			}
		}
	}
	if f.Type == "result" {
		for i := range s.tasks {
			if s.tasks[i].Status == "running" {
				s.tasks[i].Status = "completed"
				if s.tasks[i].ID == "wakeup" {
					s.tasks[i].UnrescheduledWakeups++
				}
				if s.tasks[i].Recurring && s.tasks[i].UnrescheduledWakeups < 2 {
					s.tasks[i].Status = "waiting"
				}
			}
		}
	}
	if s.known {
		f.SchedulesKnown = true
		f.Schedules = append([]ScheduledTask(nil), s.tasks...)
	}
}

func (s *scheduleTracker) list(raw json.RawMessage) {
	var result struct {
		Jobs []struct {
			ID, Prompt, Cron string
			NextFireAt       float64
			Recurring        bool
		} `json:"jobs"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Jobs == nil {
		return
	}
	s.known = true
	for i := range s.tasks {
		if s.tasks[i].ID != "wakeup" && (s.tasks[i].Status == "waiting" || s.tasks[i].Status == "running") {
			s.tasks[i].Status, s.tasks[i].NextFireAt = "stopped", 0
		}
	}
	for _, j := range result.Jobs {
		if j.ID != "" {
			s.set(ScheduledTask{ID: j.ID, Prompt: j.Prompt, Cron: j.Cron, NextFireAt: int64(j.NextFireAt), Recurring: j.Recurring, Status: "waiting"})
		}
	}
}
