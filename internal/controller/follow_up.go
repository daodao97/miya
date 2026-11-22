package controller

import (
	"slices"

	"miya/internal/chat/model"
	dg "miya/internal/delegation"
)

func (c *Controller) stopFollowUps(t model.Thread) {
	if c.Collaboration != nil {
		c.Collaboration.StopFollowUps(c.ThreadRef(t).URL())
	}
}

func (c *Controller) followUpTasks() map[string][]model.ThreadTask {
	if c.Collaboration == nil || c.Collaboration.Manager == nil {
		return nil
	}
	var tasks map[string][]model.ThreadTask
	for _, d := range c.Collaboration.Manager.FollowUps() {
		if tasks == nil {
			tasks = map[string][]model.ThreadTask{}
		}
		task := model.ThreadTask{ID: d.ID, Kind: "follow_up", Title: d.FollowUpNote, Status: "waiting", NextRunAtMs: d.FollowUpAt.UnixMilli()}
		switch d.State {
		case dg.NotifySubmitting:
			task.Status = "starting"
		case dg.NotifyNeedsAttention:
			task.Status, task.Detail = "blocked", d.Reason
		}
		tasks[d.Notify] = append(tasks[d.Notify], task)
	}
	return tasks
}

func (c *Controller) withFollowUps(t model.Thread, tasks map[string][]model.ThreadTask) model.Thread {
	if len(tasks) > 0 {
		if followUps := tasks[c.ThreadRef(t).URL()]; len(followUps) > 0 {
			t.Tasks = slices.Concat(t.Tasks, followUps)
		}
	}
	return t
}

// prepareTaskAction leaves durable timer cancellation to the worker. The
// other controls still use the native task's normal main-thread handler.
func (c *Controller) prepareTaskAction(threadID int, taskID, action string) any {
	if (action == "cancel" || action == "dismiss" || action == "stop") &&
		c.Collaboration != nil && c.Collaboration.Manager != nil {
		if d, ok := c.Collaboration.Manager.Delivery(taskID); ok && d.IsFollowUp() {
			t := c.ThreadWithID(threadID)
			if t.ID != 0 && !t.ReadOnly && d.Notify == c.ThreadRef(t).URL() {
				return c.Collaboration.CancelFollowUp(d)
			}
			return "This conversation cannot manage the follow-up."
		}
	}
	return c.TaskAction(threadID, taskID, action)
}
