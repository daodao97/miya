package main

import "miya/internal/claude"

func applyClaudeSchedules(t *Thread, schedules []claude.ScheduledTask) {
	for i, task := range t.Tasks {
		if task.ID == "loop:pending" {
			t.Tasks = append(t.Tasks[:i:i], t.Tasks[i+1:]...)
			break
		}
	}
	for _, s := range schedules {
		title := s.Prompt
		if title == "<<autonomous-loop>>" || title == "<<autonomous-loop-dynamic>>" {
			title = "自动维护"
		}
		setThreadTask(t, ThreadTask{ID: "loop:" + s.ID, Kind: "loop", Title: title,
			Status: s.Status, Detail: s.Detail, Schedule: s.Cron, NextRunAtMs: s.NextFireAt})
	}
}

func stopClaudeTasks(t *Thread, status string) {
	for i := range t.Tasks {
		if t.Tasks[i].Kind == "loop" && t.Tasks[i].Status == "starting" {
			t.Tasks[i].Status, t.Tasks[i].Detail = "failed", "未建立循环，请查看回复"
			continue
		}
		if t.Tasks[i].Kind == "loop" && (t.Tasks[i].Status == "waiting" || t.Tasks[i].Status == "running" || t.Tasks[i].Status == "starting") {
			t.Tasks[i].Status, t.Tasks[i].NextRunAtMs = status, 0
		}
	}
}
