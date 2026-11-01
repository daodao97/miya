package main

import "fmt"

func (c *chatController) taskAction(threadID int, taskID, action string) string {
	if c.remote != nil {
		var message string
		if err := c.remote.call("task", svcTaskAction{threadID, taskID, action}, &message); err != nil {
			return err.Error()
		}
		return message
	}
	t := c.threadWithID(threadID)
	if t.ID == 0 || t.ReadOnly {
		return "此对话无法管理任务。"
	}
	for _, task := range t.Tasks {
		if task.ID != taskID {
			continue
		}
		if task.Kind == "goal" && (action == "pause" || action == "resume" || action == "clear") {
			_, message := c.codexGoalCommand(sendRequest{ThreadID: threadID, Text: "/goal " + action})
			return message
		}
		if task.Kind == "loop" && action == "stop" {
			if t.TaskStopping {
				return ""
			}
			if c.submit(sendRequest{ThreadID: threadID, Steer: true, KeepSessionSettings: true, Text: "停止当前会话的所有循环任务。取消所有已安排的定时任务，结束动态循环。"}) == 0 {
				return "暂时无法停止循环，请稍后重试。"
			}
			c.update(threadID, func(t *Thread) { t.TaskStopping = true })
			return ""
		}
	}
	return fmt.Sprintf("任务 %s 已不可用。", taskID)
}
