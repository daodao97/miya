package widgets

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	"miya/internal/textutil"
)

type TaskPalette struct{ Surface, Border, Ink, Muted ui.Color }

// TaskStatusBar is shared by the desktop and phone composers. Besides loops
// and goals it lists the sub-agents still running, whichever agent started
// them, which would otherwise scroll away with the message list; each row
// leaves once its agent ends. Only a working conversation lists them: a
// launch snapshot that never got a final state must not linger after the
// turn (Claude keeps the conversation busy while background agents run).
func TaskStatusBar(c *ui.Context, t model.Thread, approval bool, p TaskPalette, action func(string, string)) {
	var agents []model.SubAgent
	if t.Busy {
		agents = subagent.ActiveAgents(t.Messages)
	}
	if len(t.Tasks) == 0 && len(agents) == 0 {
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
							if activeLoops(t) > 1 {
								name = tr("Stop all loops")
							}
							button(name, "stop", "")
						} else if model.LoopFinished(task) {
							button(tr("Clear loop"), "clear", "trash-2")
						}
					})
				}
				for i, a := range agents {
					subAgentStatusRow(c.Key("agent:"+textutil.FirstNonEmpty(a.ID, string(rune('0'+i%10)))), a, p)
				}
			})
		})
	})
}

func subAgentStatusRow(c *ui.Context, a model.SubAgent, p TaskPalette) {
	ui.Row(c).FillWidth().MinWidth(0).MinHeight(40).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
				if a.Status == subagent.Running {
					ui.Spinner(c).FontSize(12).TextColor(p.Ink)
				} else {
					Icon(c, "clock", 13, p.Muted)
				}
				ui.Text(c, tr("Subagent")+" · "+subagent.StatusLabel(a.Status)).FontSize(12).FontWeight(600).TextColor(p.Ink).Shrink(0)
				name := textutil.FirstNonEmpty(a.Name, a.ID)
				ui.Text(c, msgfmt.NativeText(name)).FontSize(12).TextColor(p.Ink).SingleLine().Ellipsis("…").Tooltip(name).Grow(1).MinWidth(0)
			})
			var parts []string
			if a.Steps > 0 {
				parts = append(parts, fmt.Sprintf(tr("%d steps"), a.Steps))
			}
			if a.Detail != "" {
				parts = append(parts, textutil.FirstLine(a.Detail))
			}
			if detail := strings.Join(parts, " · "); detail != "" {
				ui.Text(c, msgfmt.NativeText(detail)).FontSize(11).TextColor(p.Muted).SingleLine().Ellipsis("…").Tooltip(detail)
			}
		})
	})
}

func activeLoops(t model.Thread) int {
	n := 0
	for _, task := range t.Tasks {
		if task.Kind == "loop" && (task.Status == "waiting" || task.Status == "running") {
			n++
		}
	}
	return n
}
