package widgets

import (
	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

type TaskPalette struct{ Surface, Border, Ink, Muted ui.Color }

// taskStatusBar is shared by the desktop and phone composers.
func TaskStatusBar(c *ui.Context, t model.Thread, approval bool, p TaskPalette, action func(string, string)) {
	if len(t.Tasks) == 0 {
		return
	}
	ui.Column(c.Key("task-status")).FillWidth().MinWidth(0).Background(p.Surface).Border(1, p.Border).Radius(12).Children(func() {
		ui.Scroll(c.Key("task-rows")).FillWidth().MaxHeight(174).Children(func() {
			ui.Column(c).FillWidth().Gap(2).Padding(8, 12).Children(func() {
				for _, task := range t.Tasks {
					ui.Row(c.Key(task.ID)).FillWidth().MinWidth(0).MinHeight(40).Gap(10).AlignItems(ui.Center).Children(func() {
						label := appmodel.TaskStatusLabel(t, task, approval)
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
								if label == tr("Running") || label == tr("Starting") {
									ui.Spinner(c).FontSize(12).TextColor(p.Ink)
								} else {
									Icon(c, "clock", 13, p.Muted)
								}
								kind := tr("Goal")
								if task.Kind == "loop" {
									kind = tr("Loop")
								}
								ui.Text(c, kind+" · "+label).FontSize(12).FontWeight(600).TextColor(p.Ink).Shrink(0)
								ui.Text(c, msgfmt.NativeText(task.Title)).FontSize(12).TextColor(p.Ink).SingleLine().Ellipsis("…").Tooltip(task.Title).Grow(1).MinWidth(0)
							})
							if detail := appmodel.TaskDetail(task); detail != "" {
								ui.Text(c, msgfmt.NativeText(detail)).FontSize(11).TextColor(p.Muted).SingleLine().Ellipsis("…").Tooltip(detail)
							}
						})
						// glyph turns the button into a square icon button; name stays
						// its accessible label and tooltip.
						button := func(name, command, glyph string) {
							b := ui.ButtonBase(c.Key(command)).MinHeight(36).Padding(4, 8).Radius(8).Label(name).Disabled(t.ReadOnly || t.TaskPending || t.TaskStopping).Shrink(0)
							if glyph != "" {
								b.Width(36).Padding(0, 0).Row().AlignItems(ui.Center).Justify(ui.Center).Tooltip(name)
								b.Children(func() { Icon(c, glyph, 16, p.Muted) })
							} else {
								b.Children(func() { ui.Text(c, name).FontSize(12).TextColor(p.Muted) })
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
