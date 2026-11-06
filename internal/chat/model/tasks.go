package model

import ()

func ActiveGoal(t Thread) bool {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.Status == "active" {
			return true
		}
	}
	return false
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
