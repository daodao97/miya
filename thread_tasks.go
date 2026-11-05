package main

import (
	"fmt"
	"strings"
	"time"

	"miya/internal/codex"
)

func activeThreadGoal(t Thread) bool {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.Status == "active" {
			return true
		}
	}
	return false
}

func setThreadTask(t *Thread, task ThreadTask) {
	for i := range t.Tasks {
		if t.Tasks[i].ID == task.ID {
			t.Tasks[i] = task
			return
		}
	}
	t.Tasks = append(t.Tasks, task)
}

func applyCodexGoal(t *Thread, goal *codex.Goal) {
	if goal == nil {
		for i, task := range t.Tasks {
			if task.Kind == "goal" {
				t.Tasks = append(t.Tasks[:i:i], t.Tasks[i+1:]...)
				return
			}
		}
		return
	}
	setThreadTask(t, ThreadTask{ID: "goal", Kind: "goal", Title: goal.Objective, Status: goal.Status,
		TokensUsed: goal.TokensUsed, TokenBudget: goal.TokenBudget, TimeUsedSeconds: goal.TimeUsedSeconds})
}

func taskStatusLabel(t Thread, task ThreadTask, waitingApproval bool) string {
	if task.Status == "active" || task.Status == "waiting" || task.Status == "running" {
		if t.TaskStopping && task.Kind == "loop" {
			return tr("Stopping")
		}
		if waitingApproval {
			return tr("Awaiting confirmation")
		}
		if (task.Kind == "goal" && t.Busy && !t.Loading) || task.Status == "running" {
			return tr("Running")
		}
		return tr("Waiting to run")
	}
	switch task.Status {
	case "starting":
		return tr("Starting")
	case "paused":
		return tr("Paused")
	case "blocked":
		return tr("Needs attention")
	case "usageLimited":
		return tr("Quota exhausted")
	case "budgetLimited":
		return tr("Budget exhausted")
	case "complete", "completed":
		return tr("Completed")
	case "interrupted", "stopped":
		return tr("Stopped")
	case "failed":
		return tr("Failed")
	}
	return task.Status
}

func taskDetail(task ThreadTask) string {
	var parts []string
	if task.Schedule != "" {
		parts = append(parts, task.Schedule)
	}
	if task.NextRunAtMs > time.Now().UnixMilli() && (task.Status == "waiting" || task.Status == "active") {
		parts = append(parts, tr("Next ")+time.UnixMilli(task.NextRunAtMs).Format("01-02 15:04:05"))
	}
	if task.TokenBudget != nil {
		parts = append(parts, fmt.Sprintf("%d / %d tokens", task.TokensUsed, *task.TokenBudget))
	} else if task.TokensUsed > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", task.TokensUsed))
	}
	if task.Detail != "" {
		parts = append(parts, task.Detail)
	}
	return strings.Join(parts, " · ")
}

func (c *chatController) taskWaitingApproval(t Thread) bool {
	for _, a := range c.approvals() {
		if a.ThreadID == t.ServerID && t.ServerID != "" || strings.HasPrefix(string(a.ID), fmt.Sprintf("\"claude:%d:", t.ID)) {
			return true
		}
	}
	return false
}
