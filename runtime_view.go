package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	rt "miya/internal/runtime"
	"time"
)

func runtimeStatus(status string) string {
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
func (m *mobileApp) runtimeDetails(c *ui.Context, view rt.View) {
	p := m.pal
	ui.Text(c, tr("Background tasks")).FontSize(m.font(15)).FontWeight(600)
	if len(view.Tasks) == 0 {
		ui.Text(c, map[bool]string{true: tr("No native background tasks"), false: tr("Native task list unconfirmed")}[view.TasksKnown]).FontSize(m.font(14)).TextColor(p.muted)
	}
	for _, task := range view.Tasks {
		ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(14).Gap(6).Radius(14).Background(p.surface).Children(func() {
			ui.Text(c, firstNonEmpty(task.Description, tr("Background tasks"))).FontSize(m.font(15)).LineHeight(1.4)
			ui.Text(c, runtimeStatus(task.Status)+" · "+runtimeKind(task.Kind)).FontSize(m.font(13)).TextColor(p.muted)
		})
	}
	ui.Text(c, tr("System process")).FontSize(m.font(15)).FontWeight(600)
	if len(view.Processes) == 0 {
		ui.Text(c, tr("No process identity available for verification")).FontSize(m.font(14)).TextColor(p.muted)
	}
	for _, process := range view.Processes {
		ui.Text(c, fmt.Sprintf("%s · PID %d · %s", process.Name, process.Identity.PID, runtimeStatus(process.Status))).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
	}
	for _, endpoint := range view.Endpoints {
		if endpoint.Protocol != "http" {
			ui.Text(c, fmt.Sprintf("%s:%d · TCP", endpoint.Host, endpoint.Port)).FontSize(m.font(13)).TextColor(p.muted)
		}
	}
	if view.ObservedAt > 0 {
		ui.Text(c, tr("Last verified ")+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(m.font(12)).TextColor(p.muted)
	}
}

func runtimeObservationLost(result *conversationRuntime) {
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

func runtimeKind(kind string) string {
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
