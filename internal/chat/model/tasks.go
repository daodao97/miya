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

// LoopFinished reports whether a loop task has ended, so it no longer runs and
// can be cleared from the conversation like a goal.
func LoopFinished(task ThreadTask) bool {
	switch task.Status {
	case "waiting", "running", "active", "starting":
		return false
	}
	return true
}

func HasThreadTask(t Thread, id string) bool {
	for _, task := range t.Tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}

func RemoveThreadTask(t *Thread, id string) {
	for i := range t.Tasks {
		if t.Tasks[i].ID == id {
			t.Tasks = append(t.Tasks[:i:i], t.Tasks[i+1:]...)
			return
		}
	}
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
