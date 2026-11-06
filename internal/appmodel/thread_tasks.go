package appmodel

import (
	"fmt"
	"strings"
	"time"

	"miya/internal/model"
)

func TaskStatusLabel(t model.Thread, task model.ThreadTask, waitingApproval bool) string {
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

func TaskDetail(task model.ThreadTask) string {
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
