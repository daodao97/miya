package desktopui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/memory"
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
	// Query filters the Bots page's list by name and charter.
	Query string
	// reading is the Bot whose reports are being marked read; allReports
	// lists every report of the open Bot instead of the latest.
	reading    string
	allReports bool
	// profile shows the open Bot's profile beside its chat; memories are its
	// memory as last loaded, for memFor.
	profile    bool
	memFor     string
	memLoading bool
	memories   []memory.Entry
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

// botPreview is the line under a Bot's name: its last reply, else what it is
// for.
func botPreview(b bot.Bot) string {
	if len(b.Reports) > 0 && !b.Reports[0].Read {
		return b.Reports[0].Title
	}
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
	// The conversation so far stays the Bot's, with its charter.
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

// openBot opens on the Bots page the conversation the person talks to a Bot
// in, or the empty conversation its first message starts.
func (app *App) openBot(b bot.Bot) {
	app.Bots.fresh = ""
	defer func() { app.Section = "Bots" }()
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

// followBot opens a Bot's conversation once it is loaded, while the Bots page
// still shows the empty conversation opened for that Bot.
func (app *App) followBot(b bot.Bot, ok bool) {
	if !ok || app.Section != "Bots" || app.C.Current().ID != 0 {
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
		widgets.MutedText(c, tr("A Bot is a named assistant in a project. Its charter goes with every message to its conversations."))
		ui.Field(c, tr("Name"), func() {
			if ui.TextInput(c.Key("name"), &a.edit.Name).Placeholder(tr("Bot name")).AutoFocus().Submitted() {
				app.saveBot()
			}
		})
		ui.Field(c, tr("Charter"), func() {
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
		// The other projects the Bot works in, besides its own.
		ui.Field(c, tr("Also works in"), func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				for i, p := range a.edit.Projects {
					label := p
					if j := slices.Index(paths, p); j >= 0 {
						label = labels[j]
					}
					ui.Row(c.Key("mounted:"+p)).FillWidth().Height(30).Radius(8).Background(widgets.Hover).Padding(0, 4, 0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
						widgets.Icon(c, "folder", 13, widgets.Muted)
						ui.Text(c, label).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12)
						if widgets.ToolButton(c.Key("remove"), "x", fmt.Sprintf(tr("Remove %s"), label)).Clicked() {
							a.edit.Projects = slices.Delete(slices.Clone(a.edit.Projects), i, i+1)
						}
					})
				}
				var addLabels, addPaths []string
				for j, p := range paths {
					if p != a.edit.Project && !slices.Contains(a.edit.Projects, p) {
						addLabels, addPaths = append(addLabels, labels[j]), append(addPaths, p)
					}
				}
				if len(addPaths) > 0 {
					prompt := tr("Add a project…")
					choice := prompt
					if ui.Select(c.Key("add-mounted"), &choice, append([]string{prompt}, addLabels...)).Label(prompt).FillWidth().Changed() {
						if j := slices.Index(addLabels, choice); j >= 0 {
							a.edit.Projects = append(slices.Clone(a.edit.Projects), addPaths[j])
						}
					}
				}
			})
		})
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
		if others := app.reviewerChoices(a.edit); len(others) > 0 || a.edit.Reviewer != "" {
			labels, ids := []string{tr("No reviewer")}, []string{""}
			for _, b := range others {
				labels, ids = append(labels, b.Name), append(ids, b.ID)
			}
			selected := labels[0]
			for i, id := range ids {
				if id == a.edit.Reviewer {
					selected = labels[i]
				}
			}
			ui.Field(c, tr("Reviewer"), func() {
				if ui.Select(c.Key("reviewer"), &selected, labels).Label(tr("Reviewer")).FillWidth().Changed() {
					if j := slices.Index(labels, selected); j >= 0 {
						a.edit.Reviewer = ids[j]
					}
				}
			})
			widgets.MutedText(c, tr("After a turn that changes files, the reviewer checks the changes read only and the Bot fixes what it finds, up to 3 reviews in a row"))
		}
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
