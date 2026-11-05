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
								if label == tr("Running") || label == tr("Starting") {
									ui.Spinner(c).FontSize(12).TextColor(p.ink)
								} else {
									icon(c, "clock", 13, p.muted)
								}
								kind := tr("Goal")
								if task.Kind == "loop" {
									kind = tr("Loop")
								}
								ui.Text(c, kind+" · "+label).FontSize(12).FontWeight(600).TextColor(p.ink).Shrink(0)
								ui.Text(c, nativeText(task.Title)).FontSize(12).TextColor(p.ink).SingleLine().Ellipsis("…").Tooltip(task.Title).Grow(1).MinWidth(0)
							})
							if detail := taskDetail(task); detail != "" {
								ui.Text(c, nativeText(detail)).FontSize(11).TextColor(p.muted).SingleLine().Ellipsis("…").Tooltip(detail)
							}
						})
						// glyph turns the button into a square icon button; name stays
						// its accessible label and tooltip.
						button := func(name, command, glyph string) {
							b := ui.ButtonBase(c.Key(command)).MinHeight(36).Padding(4, 8).Radius(8).Label(name).Disabled(t.ReadOnly || t.TaskPending || t.TaskStopping).Shrink(0)
							if glyph != "" {
								b.Width(36).Padding(0, 0).Row().AlignItems(ui.Center).Justify(ui.Center).Tooltip(name)
								b.Children(func() { icon(c, glyph, 16, p.muted) })
							} else {
								b.Children(func() { ui.Text(c, name).FontSize(12).TextColor(p.muted) })
							}
							if b.Clicked() {
								action(task.ID, command)
							}
						}
						if task.Kind == "goal" {
							switch task.Status {
							case "active":
								button(tr("Pause goal"), "pause", "player-pause")
							case "paused", "blocked", "usageLimited", "budgetLimited":
								button(tr("Resume goal"), "resume", "player-play")
							}
							button(tr("Clear goal"), "clear", "trash-2")
						} else if task.Status == "waiting" || task.Status == "running" {
							name := tr("Stop loop")
							if len(t.Tasks) > 1 {
								name = tr("Stop all loops")
							}
							button(name, "stop", "")
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
