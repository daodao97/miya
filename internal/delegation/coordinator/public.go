package coordinator

import (
	dg "miya/internal/delegation"
)

func (s *Coordinator) WithHealth(result map[string]any) {
	if message := s.Manager.StorageError(); message != "" {
		result["storage_error"] = message
		result["stale"] = true
	}
}

func TaskPublic(task dg.Task) map[string]any {
	return map[string]any{"task_id": task.ID, "state": task.State, "thread": nullableText(task.Target), "from": task.From, "workspace": task.Workspace, "parent_task_id": task.ParentTaskID, "previous_task_id": task.PreviousTaskID, "agent": task.Agent, "title": task.Title, "revision": task.Revision, "wait_cause": task.WaitCause, "cancel_requested": task.CancelRequested, "resolution": task.Resolution, "tracking_stopped": task.Resolution == "abandoned", "progress": task.Progress, "recheck_error": task.RecheckError, "result": task.Result, "created_at": task.CreatedAt, "finished_at": task.FinishedAt}
}

func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}
