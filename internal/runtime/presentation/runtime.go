// Package presentation formats runtime observations without a UI framework.
package presentation

import (
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	"miya/locales"
)

func RuntimeStatus(status string) string {
	switch status {
	case "running", "inProgress":
		return locales.T("Running")
	case "pending":
		return locales.T("Waiting to run")
	case "exited", "ended", "completed":
		return locales.T("Ended")
	case "failed":
		return locales.T("Failed")
	case "interrupted":
		return locales.T("Stopped")
	default:
		return locales.T("Awaiting verification")
	}
}

func RuntimeObservationLost(result *tracker.ConversationRuntime) {
	result.Services = nil
	result.View.Endpoints = nil
	result.View.TasksKnown = false
	result.View.Notice = locales.T("Runtime information cannot be updated. Showing the last verified result.")
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
		return locales.T("Background command")
	case "local_agent", "remote_agent":
		return locales.T("Subagent")
	case "monitor":
		return locales.T("Monitor")
	default:
		return locales.T("Background tasks")
	}
}
