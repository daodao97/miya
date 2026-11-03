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
		return "运行中"
	case "pending":
		return "等待执行"
	case "exited", "ended", "completed":
		return "已结束"
	case "failed":
		return "失败"
	case "interrupted":
		return "已停止"
	default:
		return "待核验"
	}
}
func (m *mobileApp) runtimeDetails(c *ui.Context, view rt.View) {
	p := m.pal
	ui.Text(c, "后台任务").FontSize(m.font(15)).FontWeight(600)
	if len(view.Tasks) == 0 {
		ui.Text(c, map[bool]string{true: "当前没有原生后台任务", false: "原生任务集合尚未确认"}[view.TasksKnown]).FontSize(m.font(14)).TextColor(p.muted)
	}
	for _, task := range view.Tasks {
		ui.Column(c.Key("task-" + task.ID)).FillWidth().Padding(14).Gap(6).Radius(14).Background(p.surface).Children(func() {
			ui.Text(c, firstNonEmpty(task.Description, "后台任务")).FontSize(m.font(15)).LineHeight(1.4)
			ui.Text(c, runtimeStatus(task.Status)+" · "+runtimeKind(task.Kind)).FontSize(m.font(13)).TextColor(p.muted)
		})
	}
	ui.Text(c, "系统进程").FontSize(m.font(15)).FontWeight(600)
	if len(view.Processes) == 0 {
		ui.Text(c, "尚无可核验的进程身份").FontSize(m.font(14)).TextColor(p.muted)
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
		ui.Text(c, "最近核验 "+time.UnixMilli(view.ObservedAt).Local().Format("15:04:05")).FontSize(m.font(12)).TextColor(p.muted)
	}
}

func runtimeObservationLost(result *conversationRuntime) {
	result.Services = nil
	result.View.Endpoints = nil
	result.View.TasksKnown = false
	result.View.Notice = "运行信息暂时无法更新，以下是上次核验结果"
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
		return "后台命令"
	case "local_agent", "remote_agent":
		return "子 Agent"
	case "monitor":
		return "监控"
	default:
		return "后台任务"
	}
}
