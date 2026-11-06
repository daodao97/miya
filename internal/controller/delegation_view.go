package controller

import (
	model "miya/internal/chat/model"
)

func (c *Controller) CollaborationCapabilities() *model.CollaborationStatus {
	s := c.Collaboration
	if s == nil {
		return c.CollaborationRemote
	}
	status := &model.CollaborationStatus{Version: 1, Operations: []string{"list_agents", "start_task", "read_task", "wait_task", "send_thread", "cancel_task", "watch_task", "unwatch_task", "resolve_task", "list_tasks", "resume", "cancel_all", "resolve_delivery"}}
	if s.Manager != nil {
		status.Revision, status.StorageError = s.Manager.Revision(), s.Manager.StorageError()
	}
	if s.LoadError != nil {
		status.StorageError = s.LoadError.Error()
	}
	status.Stale = status.StorageError != ""
	if message := s.CleanupError(); message != "" {
		status.CleanupError = message
	}
	return status
}
