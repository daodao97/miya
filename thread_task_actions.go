package main

import "fmt"

func (c *chatController) taskAction(threadID int, taskID, action string) string {
	if c.remote != nil {
		var message string
		if err := c.remote.Call("task", svcTaskAction{Thread: threadID, Task: taskID, Action: action}, &message); err != nil {
			return err.Error()
		}
		return message
	}
	t := c.threadWithID(threadID)
	if t.ID == 0 || t.ReadOnly {
		return "This conversation cannot manage tasks."
	}
	for _, task := range t.Tasks {
		if task.ID != taskID {
			continue
		}
		if task.Kind == "goal" && (action == "pause" || action == "resume" || action == "clear") {
			_, message := c.codexGoalCommand(SendRequest{ThreadID: threadID, Text: "/goal " + action})
			return message
		}
		if task.Kind == "loop" && action == "stop" {
			if t.TaskStopping {
				return ""
			}
			if c.submit(SendRequest{ThreadID: threadID, Steer: true, KeepSessionSettings: true, Text: "Stop all looping tasks in the current session. Cancel all scheduled tasks and end dynamic loops."}) == 0 {
				return "Unable to stop the loop. Try again later."
			}
			c.update(threadID, func(t *Thread) { t.TaskStopping = true })
			return ""
		}
	}
	return fmt.Sprintf("Task %s is no longer available.", taskID)
}
