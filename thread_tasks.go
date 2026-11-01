package main

import (
	"fmt"
	"strings"
	"time"

	"miya/internal/codex"
)

// ThreadTask is the common view of native goals and session schedules. The
// agent owns execution; Miya only projects its reported state and controls.
type ThreadTask struct {
	ID, Kind, Title, Status, Detail string
	Schedule                        string `json:",omitempty"`
	NextRunAtMs                     int64  `json:",omitempty"`
	TokensUsed, TimeUsedSeconds     int64  `json:",omitempty"`
	TokenBudget                     *int64 `json:",omitempty"`
}

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
			return "正在停止"
		}
		if waitingApproval {
			return "等待确认"
		}
		if (task.Kind == "goal" && t.Busy && !t.Loading) || task.Status == "running" {
			return "运行中"
		}
		return "等待执行"
	}
	switch task.Status {
	case "starting":
		return "正在启动"
	case "paused":
		return "已暂停"
	case "blocked":
		return "需要处理"
	case "usageLimited":
		return "额度已用尽"
	case "budgetLimited":
		return "预算已用尽"
	case "complete", "completed":
		return "已完成"
	case "interrupted", "stopped":
		return "已停止"
	case "failed":
		return "执行失败"
	}
	return task.Status
}

func taskDetail(task ThreadTask) string {
	var parts []string
	if task.Schedule != "" {
		parts = append(parts, task.Schedule)
	}
	if task.NextRunAtMs > time.Now().UnixMilli() && (task.Status == "waiting" || task.Status == "active") {
		parts = append(parts, "下次 "+time.UnixMilli(task.NextRunAtMs).Format("01-02 15:04:05"))
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
