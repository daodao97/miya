package main

import (
	"github.com/egoist/mygo/ui"
)

type taskPalette struct{ surface, border, ink, muted ui.Color }

// taskStatusBar is shared by the desktop and phone composers.
func taskStatusBar(c *ui.Context, t Thread, approval bool, p taskPalette, action func(string, string)) {
	if len(t.Tasks) == 0 {
		return
	}
	ui.Column(c.Key("task-status")).FillWidth().MinWidth(0).Background(p.surface).Border(1, p.border).Radius(12).Children(func() {
		ui.Scroll(c.Key("task-rows")).FillWidth().MaxHeight(174).Children(func() {
			ui.Column(c).FillWidth().Gap(2).Padding(8, 12).Children(func() {
				for _, task := range t.Tasks {
					ui.Row(c.Key(task.ID)).FillWidth().MinWidth(0).MinHeight(40).Gap(10).AlignItems(ui.Center).Children(func() {
						label := taskStatusLabel(t, task, approval)
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
								if label == "运行中" || label == "正在启动" {
									ui.Spinner(c).FontSize(12).TextColor(p.ink)
								} else {
									icon(c, "clock", 13, p.muted)
								}
								kind := "Goal"
								if task.Kind == "loop" {
									kind = "Loop"
								}
								ui.Text(c, kind+" · "+label).FontSize(12).FontWeight(600).TextColor(p.ink).Shrink(0)
								ui.Text(c, nativeText(task.Title)).FontSize(12).TextColor(p.ink).SingleLine().Ellipsis("…").Tooltip(task.Title).Grow(1).MinWidth(0)
							})
							if detail := taskDetail(task); detail != "" {
								ui.Text(c, nativeText(detail)).FontSize(11).TextColor(p.muted).SingleLine().Ellipsis("…").Tooltip(detail)
							}
						})
						button := func(name, command string) {
							b := ui.ButtonBase(c.Key(command)).MinHeight(36).Padding(4, 8).Radius(8).Label(name).Disabled(t.ReadOnly || t.TaskPending || t.TaskStopping).Shrink(0)
							b.Children(func() { ui.Text(c, name).FontSize(12).TextColor(p.muted) })
							if b.Clicked() {
								action(task.ID, command)
							}
						}
						if task.Kind == "goal" {
							switch task.Status {
							case "active":
								button("暂停目标", "pause")
							case "paused", "blocked", "usageLimited", "budgetLimited":
								button("继续目标", "resume")
							}
							button("清除目标", "clear")
						} else if task.Status == "waiting" || task.Status == "running" {
							name := "停止循环"
							if len(t.Tasks) > 1 {
								name = "停止全部循环"
							}
							button(name, "stop")
						}
					})
				}
			})
		})
	})
}

func (app *chatApp) taskStatus(c *ui.Context, t Thread) {
	taskStatusBar(c, t, app.c.taskWaitingApproval(t), taskPalette{panel, line, ink, muted}, func(id, action string) {
		app.notice = app.c.taskAction(t.ID, id, action)
	})
}

func (m *mobileApp) taskStatus(c *ui.Context) {
	t := m.view.c.current()
	p := m.pal
	taskStatusBar(c, t, len(m.approvalsFor(t.ServerID)) > 0, taskPalette{p.surface, p.line, p.ink, p.muted}, func(id, action string) {
		if m.conn == nil || !m.connected {
			m.sendErr = "未连接桌面端"
			return
		}
		r := m.threadRequest("task", t.ID)
		r.Text = id + " " + action
		go m.conn.Send(r)
	})
}
