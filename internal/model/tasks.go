package model

import (
	"miya/internal/agent/codex/native"
)

func ActiveGoal(t Thread) bool {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.Status == "active" {
			return true
		}
	}
	return false
}

func ApplyCodexGoal(t *Thread, goal *codex.Goal) {
	if goal == nil {
		for i, task := range t.Tasks {
			if task.Kind == "goal" {
				t.Tasks = append(t.Tasks[:i:i], t.Tasks[i+1:]...)
				return
			}
		}
		return
	}
	SetThreadTask(t, ThreadTask{ID: "goal", Kind: "goal", Title: goal.Objective, Status: goal.Status,
		TokensUsed: goal.TokensUsed, TokenBudget: goal.TokenBudget, TimeUsedSeconds: goal.TimeUsedSeconds})
}

func SetThreadTask(t *Thread, task ThreadTask) {
	for i := range t.Tasks {
		if t.Tasks[i].ID == task.ID {
			t.Tasks[i] = task
			return
		}
	}
	t.Tasks = append(t.Tasks, task)
}
