package desktopui

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/widgets"
)

// botsUI is the Bot dialog's state.
type botsUI struct {
	open   bool
	edit   bot.Bot
	err    string
	saving bool
	// fresh is the Bot just given a new conversation: the service's list may
	// name the old one for a moment longer.
	fresh string
}

// botCall runs a Bot operation. Through the service it waits off the main
// thread and finishes on it.
func (app *App) botCall(op string, r bot.Request, done func(bot.Bot, error)) {
	if app.C.State().Remote == nil {
		done(app.C.BotOp(op, r))
		return
	}
	go func() {
		b, err := app.C.BotOp(op, r)
		app.C.Dispatch(func() { done(b, err) })
	}()
}

// sidebarBots lists the Bots above the projects. Opening one opens its
// standing conversation, like any other conversation.
func (app *App) sidebarBots(c *ui.Context) {
	current, open := app.C.CurrentBot()
	app.followBot(current, open)
	ui.Row(c).AlignItems(ui.Center).Margin(28, 0, 4, 0).Padding(0, 12, 0, 20).Children(func() {
		ui.Text(c, tr("Bots")).FontSize(11).TextColor(widgets.Faint).Grow(1)
		if widgets.ToolButton(c.Key("add-bot"), "plus", tr("New Bot")).Clicked() {
			app.editBot(bot.Bot{})
		}
	})
	ui.Column(c).PaddingX(12).Gap(3).Children(func() {
		for _, b := range app.C.Bots() {
			app.botRow(c.Key("bot:"+b.ID), b, open && current.ID == b.ID)
		}
	})
}

// botRow shows a Bot: its name, the start of its last reply, and the state of
// its standing conversation.
func (app *App) botRow(c *ui.Context, b bot.Bot, active bool) {
	t := app.C.BotThread(b)
	wrap := ui.Box(c).FillWidth()
	hovered := wrap.Hovered()
	running := t.ID != 0 && t.Busy && !t.Loading
	unread := t.ID != 0 && !t.Busy && app.C.DesktopThreadUnread(t)
	wrap.ContextMenu(func(m *ui.Menu) { app.botMenu(m, b) })
	wrap.Children(func() {
		row := widgets.Quiet(c.Key("row")).FillWidth().Height(44).FontSize(13).Clip()
		if active {
			row.Background(widgets.Chosen)
		}
		row.Children(func() {
			widgets.AgentIcon(c, b.Agent, 16)
			text := ui.Column(c).Grow(1).MinWidth(0).Gap(1).Clip()
			if hovered || running || unread {
				text.Margin(0, 24, 0, 0)
			}
			text.Children(func() {
				ui.Text(c, b.Name).NoWrap().FontWeight(500).TextColor(widgets.Ink)
				ui.Text(c, botPreview(b)).NoWrap().FontSize(11).TextColor(widgets.Faint)
			})
		})
		if row.Clicked() {
			app.openBot(b)
		}
		ui.Row(c.Key("status-actions")).Absolute().Right(5).Top(10).Size(24, 24).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if !hovered {
				if running {
					ui.Spinner(c.Key("running")).Size(16, 16).FontSize(13).Label(tr("Working…"))
				} else if unread {
					color := ui.Hex("#30a46c")
					if app.Dark {
						color = ui.Hex("#3dd68c")
					}
					ui.Box(c.Key("unread")).Size(8, 8).Radius(4).Background(color).Label(tr("Unread: ") + b.Name)
				}
			}
			// Keep the anchor built while the pointer moves into its menu.
			more := widgets.Quiet(c.Key("more")).Absolute().Top(0).Left(0).Size(24, 24).PaddingX(4).Justify(ui.Center).Background(widgets.Hover).Label(tr("Bot actions")).Tooltip(tr("More"))
			if active {
				more.Background(widgets.Chosen)
			}
			if !hovered {
				more.Invisible()
			}
			more.Children(func() { widgets.Icon(c, "dots", 16, widgets.Muted) })
			more.PopoverMenuButton(func(m *ui.Menu) { app.botMenu(m, b) })
		})
	})
}

// botPreview is the line under a Bot's name: its last reply, else what it is
// for.
func botPreview(b bot.Bot) string {
	for _, text := range []string{b.Preview, b.Note} {
		if line := strings.Join(strings.Fields(text), " "); line != "" {
			return line
		}
	}
	return tr("No messages yet")
}

func (app *App) botMenu(m *ui.Menu, b bot.Bot) {
	if m.Item(tr("Edit Bot")).Chosen() {
		app.editBot(b)
	}
	// The conversation so far stays in its project as an ordinary one.
	if (b.Session != "" || b.Thread != 0) && m.Item(tr("Start a new conversation")).Chosen() {
		app.botCall("botReset", bot.Request{ID: b.ID}, func(_ bot.Bot, err error) {
			if err != nil {
				app.C.State().SetStatus(err.Error())
				return
			}
			b.Session, b.Thread, b.Preview = "", 0, ""
			app.openBot(b)
			app.Bots.fresh = b.ID
		})
	}
	m.Separator()
	if m.Item(tr("Delete Bot")).Chosen() {
		app.botCall("botDelete", bot.Request{ID: b.ID}, func(_ bot.Bot, err error) {
			if err != nil {
				app.C.State().SetStatus(err.Error())
			}
		})
	}
}

// openBot opens a Bot's standing conversation, or the empty conversation its
// first message starts.
func (app *App) openBot(b bot.Bot) {
	app.Bots.fresh = ""
	if t := app.C.BotThread(b); t.ID != 0 {
		app.SelectThread(t)
		return
	}
	if path := b.Project; path != "" && path != app.C.State().Cwd {
		if err := app.C.ChangeWorkspace(path); err != nil {
			app.C.State().SetStatus("Project selection failed: " + err.Error())
			return
		}
		app.Workspace, app.ProjectPath, app.ExpandedProject = path, path, path
		for _, p := range app.C.State().Projects() {
			if p.Path == path {
				app.ProjectName = p.Name
			}
		}
	}
	// A new conversation forgets the Bot it was for, so name the Bot after it.
	app.newThread()
	app.C.SetDraftBot(b.ID)
}

// followBot opens a Bot's standing conversation once it is loaded, while the
// window still shows the empty conversation opened for that Bot.
func (app *App) followBot(b bot.Bot, ok bool) {
	if !ok || app.Section != "" || app.C.Current().ID != 0 {
		return
	}
	if app.Bots.fresh == b.ID {
		if b.Session != "" || b.Thread != 0 {
			return
		}
		app.Bots.fresh = ""
	}
	if strings.TrimSpace(app.C.State().Draft()) != "" {
		return
	}
	if t := app.C.BotThread(b); t.ID != 0 {
		app.SelectThread(t)
	}
}

// editBot opens the dialog for a Bot, or for a new one in the open project.
func (app *App) editBot(b bot.Bot) {
	if b.ID == "" {
		b.Agent = app.C.CurrentAgent()
		b.Project = app.C.State().Cwd
	}
	app.Bots.open, app.Bots.edit, app.Bots.err, app.Bots.saving = true, b, "", false
	app.closeMenus()
}

func (app *App) saveBot() {
	a := &app.Bots
	if a.saving {
		return
	}
	b := a.edit
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		a.err = tr("Give the Bot a name")
		return
	}
	created := b.ID == ""
	a.saving, a.err = true, ""
	app.botCall("botSave", bot.Request{ID: b.ID, Bot: b}, func(saved bot.Bot, err error) {
		a.saving = false
		if err != nil {
			a.err = err.Error()
			return
		}
		a.open = false
		if created {
			app.openBot(saved)
		}
	})
}

// botProjects lists the projects a Bot may live in: the picker's labels and
// the path each stands for. A repeated name shows its path too.
func (app *App) botProjects(current string) (labels, paths []string) {
	projects := app.C.State().Projects()
	names := map[string]int{}
	for _, p := range projects {
		names[p.Name]++
	}
	for _, p := range projects {
		label := p.Name
		if label == "" || names[p.Name] > 1 {
			label = strings.TrimSpace(p.Name + " " + p.Path)
		}
		labels, paths = append(labels, label), append(paths, p.Path)
	}
	if current != "" && !slices.Contains(paths, current) {
		labels, paths = append(labels, current), append(paths, current)
	}
	return labels, paths
}

// botAccessLevels are the levels the dialog offers, with the Bot's own when it
// holds another.
func botAccessLevels(current string) []automation.Access {
	levels := []automation.Access{"", automation.AccessRead, automation.AccessFull}
	if level := automation.Access(current); !slices.Contains(levels, level) {
		levels = append(levels, level)
	}
	return levels
}

func (app *App) botDialogView(c *ui.Context) {
	a := &app.Bots
	ui.DialogBase(c, &a.open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Width(520).MaxWidthPercent(90).Padding(24).Gap(16).Background(widgets.Panel).Border(1, widgets.Border).Radius(14).
			Shadow(0, 18, 48, 0, ui.RGBA(0, 0, 0, 0.18)).TextColor(widgets.Ink)
		title := tr("New Bot")
		if a.edit.ID != "" {
			title = tr("Edit Bot")
		}
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(18).FontWeight(600).Grow(1)
			if widgets.ToolButton(c.Key("close"), "x", tr("Close")).Clicked() {
				a.open = false
			}
		})
		widgets.MutedText(c, tr("A Bot is a named assistant with one standing conversation in a project."))
		ui.Field(c, tr("Name"), func() {
			if ui.TextInput(c.Key("name"), &a.edit.Name).Placeholder(tr("Bot name")).AutoFocus().Submitted() {
				app.saveBot()
			}
		})
		ui.Field(c, tr("Role"), func() {
			ui.TextArea(c.Key("note"), &a.edit.Note).Placeholder(tr("What this Bot is for and how it should work")).Lines(3, 8).FillWidth()
		})
		ui.Field(c, tr("Agent"), func() {
			ui.Row(c).Gap(6).Children(func() {
				for _, ag := range model.Backends {
					if ag != a.edit.Agent && !slices.Contains(app.C.EnabledAgents(), ag) {
						continue
					}
					pick := widgets.Quiet(c.Key("agent:" + ag)).Height(28).Gap(6).PaddingX(9).Label(model.AgentName(ag))
					if a.edit.Agent == ag {
						pick.Background(widgets.SelectedBg)
					}
					pick.Children(func() {
						widgets.AgentIcon(c, ag, 14)
						ui.Text(c, model.AgentName(ag)).FontWeight(500).SingleLine()
					})
					if pick.Clicked() && a.edit.Agent != ag {
						// A model belongs to its agent.
						a.edit.Agent, a.edit.Model, a.edit.Effort = ag, "", ""
					}
				}
			})
		})
		labels, paths := app.botProjects(a.edit.Project)
		if i := slices.Index(paths, a.edit.Project); i >= 0 {
			selected := labels[i]
			ui.Field(c, tr("Project"), func() {
				if ui.Select(c.Key("project"), &selected, labels).Label(tr("Project")).FillWidth().Changed() {
					if j := slices.Index(labels, selected); j >= 0 {
						a.edit.Project = paths[j]
					}
				}
			})
		}
		levels := botAccessLevels(a.edit.Access)
		names := make([]string, len(levels))
		for i, level := range levels {
			names[i] = automationAccessLabel(level)
		}
		access := names[slices.Index(levels, automation.Access(a.edit.Access))]
		ui.Field(c, tr("Access"), func() {
			if ui.Select(c.Key("access"), &access, names).Label(tr("Access")).FillWidth().Changed() {
				if j := slices.Index(names, access); j >= 0 {
					a.edit.Access = string(levels[j])
				}
			}
		})
		if a.edit.Session != "" {
			widgets.MutedText(c, tr("Changing the agent or project starts a new conversation."))
		}
		if a.err != "" {
			ui.Text(c, a.err).FontSize(12).TextColor(widgets.WarnInk)
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				a.open = false
			}
			if ui.PrimaryButton(c.Key("save"), tr("Save")).Clicked() {
				app.saveBot()
			}
		})
	})
}
