package claude

import (
	"miya/internal/chat/model"
)

func ApplySchedules(t *model.Thread, schedules []ScheduledTask) {
	for i, task := range t.Tasks {
		if task.ID == "loop:pending" {
			t.Tasks = append(t.Tasks[:i:i], t.Tasks[i+1:]...)
			break
		}
	}
	for _, s := range schedules {
		// The session tracker keeps reporting ended schedules. Once the user
		// clears one, do not bring it back; a new wakeup arrives as waiting.
		if model.LoopFinished(model.ThreadTask{Status: s.Status}) && !model.HasThreadTask(*t, "loop:"+s.ID) {
			continue
		}
		title := s.Prompt
		if title == "<<autonomous-loop>>" || title == "<<autonomous-loop-dynamic>>" {
			title = "Automatic maintenance"
		}
		model.SetThreadTask(t, model.ThreadTask{ID: "loop:" + s.ID, Kind: "loop", Title: title,
			Status: s.Status, Detail: s.Detail, Schedule: s.Cron, NextRunAtMs: s.NextFireAt})
	}
}

func StopTasks(t *model.Thread, status string) {
	for i := range t.Tasks {
		if t.Tasks[i].Kind == "loop" && t.Tasks[i].Status == "starting" {
			t.Tasks[i].Status, t.Tasks[i].Detail = "failed", "No loop was created. Check the response."
			continue
		}
		if t.Tasks[i].Kind == "loop" && (t.Tasks[i].Status == "waiting" || t.Tasks[i].Status == "running" || t.Tasks[i].Status == "starting") {
			t.Tasks[i].Status, t.Tasks[i].NextRunAtMs = status, 0
		}
	}
}
