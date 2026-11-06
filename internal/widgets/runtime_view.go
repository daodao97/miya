package widgets

import (
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
)

func RuntimeStatus(status string) string {
	switch status {
	case "running", "inProgress":
		return tr("Running")
	case "pending":
		return tr("Waiting to run")
	case "exited", "ended", "completed":
		return tr("Ended")
	case "failed":
		return tr("Failed")
	case "interrupted":
		return tr("Stopped")
	default:
		return tr("Awaiting verification")
	}
}

func RuntimeObservationLost(result *tracker.ConversationRuntime) {
	result.Services = nil
	result.View.Endpoints = nil
	result.View.TasksKnown = false
	result.View.Notice = tr("Runtime information cannot be updated. Showing the last verified result.")
	for i := range result.View.Tasks {
		if rt.Active(result.View.Tasks[i].Status) {
			result.View.Tasks[i].Status = "unknown"
		}
	}
	for i := range result.View.Processes {
		if result.View.Processes[i].Status == "running" {
			result.View.Processes[i].Status = "unknown"
		}
	}
}

func RuntimeKind(kind string) string {
	switch kind {
	case "shell", "local_bash":
		return tr("Background command")
	case "local_agent", "remote_agent":
		return tr("Subagent")
	case "monitor":
		return tr("Monitor")
	default:
		return tr("Background tasks")
	}
}
