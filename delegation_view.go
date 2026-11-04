package main

// Only capability and revision metadata travels with chat state. Task bodies,
// context snapshots and results are fetched explicitly, never broadcast.
type collaborationStatus struct {
	Version      int      `json:"version"`
	Revision     uint64   `json:"revision"`
	Operations   []string `json:"operations"`
	StorageError string   `json:"storage_error,omitempty"`
	Stale        bool     `json:"stale,omitempty"`
	CleanupError string   `json:"cleanup_error,omitempty"`
}

func (c *chatController) collaborationCapabilities() *collaborationStatus {
	s := c.collaboration
	if s == nil {
		return c.collaborationRemote
	}
	status := &collaborationStatus{Version: 1, Operations: []string{"list_agents", "start_task", "read_task", "wait_task", "send_thread", "cancel_task", "watch_task", "unwatch_task", "resolve_task", "list_tasks", "resume", "cancel_all", "resolve_delivery"}}
	if s.manager != nil {
		status.Revision, status.StorageError = s.manager.Revision(), s.manager.StorageError()
	}
	if s.loadError != nil {
		status.StorageError = s.loadError.Error()
	}
	status.Stale = status.StorageError != ""
	if message := s.cleanupError.Load(); message != nil {
		status.CleanupError = *message
	}
	return status
}
