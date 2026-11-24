package desktopui

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/widgets"
)

// The Bots page is an instant-messaging view of the Bots: the Bots like
// contacts on the left, the open one's conversation as a chat on the right
// (docs/personal-agent.md, P4). Sending, approvals and history are those of
// the window's open conversation.

// ShowBots opens the Bots page, with a Bot open when there is one.
func (app *App) ShowBots() {
	app.Section = "Bots"
	app.closeMenus()
	if _, ok := app.C.CurrentBot(); !ok {
		if bots := app.C.Bots(); len(bots) > 0 {
			app.openBot(bots[0])
		}
	}
}

// inConversation reports whether the window shows its open conversation: the
// chat, or a Bot's conversation on the Bots page.
func (app *App) inConversation() bool {
	if app.Section == "Bots" {
		_, ok := app.C.CurrentBot()
		return ok
	}
	return app.Section == ""
}

func (app *App) botsPage(c *ui.Context) {
	current, open := app.C.CurrentBot()
	app.followBot(current, open)
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		app.botContacts(c.Key("contacts"), current, open)
		defer func() {
			if open && app.Bots.profile {
				app.botProfile(c.Key("profile"), current)
			}
		}()
		ui.Column(c.Key("chat")).Grow(1).MinWidth(0).FillHeight().Children(func() {
			if !open {
				app.botsEmpty(c)
				return
			}
			app.botChatHeader(c.Key("chat-header"), current)
			app.botReports(c.Key("reports"), current)
			// Seeing the Bot reads its reports.
			if current.Unread() > 0 && app.Bots.reading != current.ID {
				app.Bots.reading = current.ID
				app.botCall("botRead", bot.Request{ID: current.ID}, func(bot.Bot, error) { app.Bots.reading = "" })
			}
			// The chat, its footer rows and the composer share one width.
			app.Bubbles = true
			app.Avatar = func(c *ui.Context) { botAvatar(c, current, 32) }
			if cur := app.C.Current(); cur.ID != 0 {
				app.MessageList(c, cur)
			} else {
				app.botIntro(c, current)
			}
			app.composerArea(c)
			app.Bubbles, app.Avatar = false, nil
		})
	})
}

// botContacts lists the Bots like contacts, filtered by the search.
func (app *App) botContacts(c *ui.Context, current bot.Bot, open bool) {
	ui.Column(c).Width(288).Shrink(0).FillHeight().Background(widgets.Side).BorderWidth(0, 1, 0, 0).BorderColor(widgets.Line).Children(func() {
		ui.Row(c).Height(52).Shrink(0).Padding(6, 10, 0, 18).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Bots")).FontSize(20).FontWeight(700).Grow(1)
			if widgets.ToolButton(c.Key("add-bot"), "plus", tr("New Bot")).Clicked() {
				app.editBot(bot.Bot{})
			}
		})
		ui.Row(c).Shrink(0).Padding(4, 14, 10, 14).Children(func() {
			ui.Row(c.Key("search")).FillWidth().Height(30).Radius(8).Background(widgets.Hover).PaddingX(9).Gap(6).AlignItems(ui.Center).Children(func() {
				widgets.Icon(c, "search", 13, widgets.Faint)
				ui.TextInput(c.Key("query"), &app.Bots.Query).Placeholder(tr("Search Bots")).Grow(1).MinWidth(0).Background(ui.RGBA(0, 0, 0, 0)).Border(0, widgets.Hover).FontSize(13)
			})
		})
		ui.Scroll(c.Key("list")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).PaddingX(8).Gap(2).Children(func() {
				for _, b := range app.C.Bots() {
					if botMatches(b, app.Bots.Query) {
						app.botContact(c.Key("bot:"+b.ID), b, open && current.ID == b.ID)
					}
				}
			})
		})
	})
}

// botContact is a Bot's row: its avatar, its name, the start of its last
// reply, and whether it works or has an unread reply.
func (app *App) botContact(c *ui.Context, b bot.Bot, active bool) {
	t := app.C.BotThread(b)
	running := t.ID != 0 && t.Busy && !t.Loading
	unread := t.ID != 0 && !t.Busy && app.C.DesktopThreadUnread(t) || b.Unread() > 0
	wrap := ui.Box(c).FillWidth()
	wrap.ContextMenu(func(m *ui.Menu) { app.botMenu(m, b) })
	wrap.Children(func() {
		row := widgets.Quiet(c.Key("row")).FillWidth().Height(60).Radius(10).Gap(11).PaddingX(10).Clip()
		if active {
			row.Background(widgets.Accent.Mix(widgets.Side, 0.84))
		}
		row.Children(func() {
			botAvatar(c, b, 40)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, b.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(13).FontWeight(600).TextColor(widgets.Ink)
					if running {
						ui.Spinner(c.Key("running")).Size(13, 13).FontSize(11).Label(tr("Working…"))
					} else if unread {
						ui.Box(c.Key("unread")).Size(9, 9).Radius(4.5).Background(widgets.Accent).Label(tr("Unread: ") + b.Name)
					}
				})
				ui.Text(c, botPreview(b)).SingleLine().Ellipsis("…").FontSize(12).TextColor(widgets.Muted)
			})
		})
		if row.Clicked() {
			app.openBot(b)
		}
	})
}

// botChatHeader names the Bot of the open chat: its agent, its reviewer and
// whether it works now, and holds its menu.
func (app *App) botChatHeader(c *ui.Context, b bot.Bot) {
	working := false
	if t := app.C.BotThread(b); t.ID != 0 && t.Busy {
		working = true
	}
	reviewer := ""
	for _, x := range app.C.Bots() {
		if x.ID == b.Reviewer && b.Reviewer != "" {
			reviewer = x.Name
		}
	}
	ui.Row(c).Height(60).Shrink(0).Padding(0, 14, 0, 22).Gap(11).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(widgets.Line).Children(func() {
		botAvatar(c, b, 34)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, b.Name).FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				widgets.AgentIcon(c, b.Agent, 11)
				ui.Text(c, model.AgentName(b.Agent)).FontSize(11).TextColor(widgets.Muted).SingleLine()
				if len(b.Projects) > 0 {
					ui.Text(c, "·").FontSize(11).TextColor(widgets.Faint)
					ui.Text(c, fmt.Sprintf(tr("%d projects"), len(b.AllProjects()))).FontSize(11).TextColor(widgets.Muted).SingleLine()
				}
				if reviewer != "" {
					ui.Text(c, "·").FontSize(11).TextColor(widgets.Faint)
					ui.Text(c, fmt.Sprintf(tr("Reviewed by %s"), reviewer)).FontSize(11).TextColor(widgets.Muted).SingleLine()
				}
				if working {
					ui.Text(c, "·").FontSize(11).TextColor(widgets.Faint)
					ui.Text(c, tr("Working…")).FontSize(11).TextColor(widgets.Accent).SingleLine()
				}
			})
		})
		profile := widgets.ToolButton(c.Key("profile"), "layout-sidebar-right", tr("Bot profile"))
		if app.Bots.profile {
			profile.Background(widgets.Chosen)
		}
		if profile.Clicked() {
			app.Bots.profile = !app.Bots.profile
		}
		widgets.ToolButton(c.Key("more"), "dots", tr("Bot actions")).PopoverMenuButton(func(m *ui.Menu) { app.botMenu(m, b) })
	})
}

// botIntro stands in for the chat before the first message: who the Bot is
// and what it is for.
func (app *App) botIntro(c *ui.Context, b bot.Bot) {
	ui.Column(c).Grow(1).MinHeight(100).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Padding(32).Children(func() {
		botAvatar(c, b, 72)
		ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, b.Name).FontSize(22).FontWeight(700)
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				widgets.AgentIcon(c, b.Agent, 12)
				ui.Text(c, model.AgentName(b.Agent)).FontSize(12).TextColor(widgets.Muted)
			})
		})
		if b.Note != "" {
			ui.Column(c).MaxWidth(460).Background(widgets.Bubble).Radius(14).Padding(12, 16, 12, 16).Children(func() {
				ui.Text(c, b.Note).MaxLines(8).FontSize(13).TextColor(widgets.Ink)
			})
		}
		widgets.MutedText(c, tr("Your first message starts a conversation with this Bot."))
	})
}

func (app *App) botsEmpty(c *ui.Context) {
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(14).Children(func() {
		ui.Box(c).Size(64, 64).Radius(32).Background(widgets.Hover).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			widgets.Icon(c, "message-circle", 30, widgets.Faint)
		})
		widgets.MutedText(c, tr("Pick a Bot to chat with, or create one."))
		if ui.Button(c.Key("new-bot"), tr("New Bot")).Clicked() {
			app.editBot(bot.Bot{})
		}
	})
}

// avatarColors are the system colours that tell Bots apart; each reads with
// white text in both appearances.
var avatarColors = []string{"#ff9500", "#007aff", "#34c759", "#5856d6", "#ff2d55", "#30b0c7", "#af52de", "#ff3b30"}

// botAvatar is a Bot's initial on a colour its ID picks, so a renamed Bot
// keeps its colour.
func botAvatar(c *ui.Context, b bot.Bot, size float32) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(b.ID))
	color := ui.Hex(avatarColors[h.Sum32()%uint32(len(avatarColors))])
	ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).Background(color).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Text(c, botInitial(b.Name)).FontSize(size * 0.44).FontWeight(600).TextColor(ui.Hex("#ffffff"))
	})
}

func botInitial(name string) string {
	for _, r := range name {
		return string(unicode.ToUpper(r))
	}
	return "?"
}

// botMatches reports whether a Bot's name or charter holds the search.
func botMatches(b bot.Bot, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	return query == "" || strings.Contains(strings.ToLower(b.Name), query) || strings.Contains(strings.ToLower(b.Note), query)
}

// projectBots are the Bots of a project, for an automation to be given to.
func (app *App) projectBots(project string) []bot.Bot {
	var out []bot.Bot
	for _, b := range app.C.Bots() {
		if b.WorksIn(project) {
			out = append(out, b)
		}
	}
	return out
}

// reviewerChoices are the Bots that may review b: the others of its project,
// those with another agent first, as a second opinion.
func (app *App) reviewerChoices(b bot.Bot) []bot.Bot {
	var other, same []bot.Bot
	for _, x := range app.C.Bots() {
		if x.ID == b.ID || x.Project != b.Project {
			continue
		}
		if x.Agent != b.Agent {
			other = append(other, x)
		} else {
			same = append(same, x)
		}
	}
	return append(other, same...)
}

// botReports are the Bot's latest reports at the top of its chat: what it
// did on its own, each opening the conversation of the work.
func (app *App) botReports(c *ui.Context, b bot.Bot) {
	if len(b.Reports) == 0 {
		return
	}
	shown := b.Reports
	if !app.Bots.allReports && len(shown) > 2 {
		shown = shown[:2]
	}
	ui.Row(c).FillWidth().Shrink(0).Justify(ui.Center).Padding(12, 22, 6, 22).BorderWidth(0, 0, 1, 0).BorderColor(widgets.Line).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(app.ContentWidth()).Gap(6).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, tr("Reports")).FontSize(11).FontWeight(600).TextColor(widgets.Muted).Grow(1)
				if len(b.Reports) > 2 {
					label := fmt.Sprintf(tr("All %d"), len(b.Reports))
					if app.Bots.allReports {
						label = tr("Fewer")
					}
					more := widgets.Quiet(c.Key("all")).Height(22).PaddingX(6).Label(label)
					more.Children(func() { ui.Text(c, label).FontSize(11).TextColor(widgets.Accent) })
					if more.Clicked() {
						app.Bots.allReports = !app.Bots.allReports
					}
				}
			})
			list := ui.Scroll(c.Key("list")).FillWidth().MaxHeight(280)
			list.Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					for i, r := range shown {
						app.botReport(c.Key(fmt.Sprint("report:", i, r.Session)), r)
					}
				})
			})
		})
	})
}

// botReport is one report: what kind of work, its title and when, whether it
// needs the person, and the start of its result.
func (app *App) botReport(c *ui.Context, r bot.Report) {
	t := app.C.BotReportThread(r)
	icon := "message-circle"
	if r.Kind == "automation" {
		icon = "clock"
	}
	card := widgets.Quiet(c).FillWidth().Height(0).MinHeight(52).Radius(12).Background(widgets.Bubble).Padding(9, 12, 9, 10).Gap(10).AlignItems(ui.Center).Label(r.Title)
	card.Children(func() {
		ui.Box(c).Size(28, 28).Shrink(0).Radius(14).Background(widgets.Accent.Mix(widgets.Canvas, 0.85)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			widgets.Icon(c, icon, 14, widgets.Accent)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.Title).FontSize(13).FontWeight(600).TextColor(widgets.Ink).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				if r.NeedsUser {
					ui.Box(c).Radius(6).Background(widgets.WarnBg).Padding(1, 6, 1, 6).Shrink(0).Children(func() {
						ui.Text(c, tr("Needs you")).FontSize(10).FontWeight(600).TextColor(widgets.WarnInk)
					})
				}
				ui.Text(c, msgfmt.FormatMessageTimestamp(r.At.UnixMilli())).FontSize(11).TextColor(widgets.Faint).Shrink(0)
			})
			if r.Summary != "" {
				ui.Text(c, r.Summary).FontSize(12).TextColor(widgets.Muted).MaxLines(2)
			}
		})
	})
	if card.Clicked() && t.ID != 0 {
		app.SelectThread(t)
	}
}

// botProfile is who a Bot is, beside its chat: its charter, its duties (the
// automations given to it), what it remembers and where it works.
func (app *App) botProfile(c *ui.Context, b bot.Bot) {
	if app.Bots.memFor != b.ID && !app.Bots.memLoading {
		app.loadBotMemory(b)
	}
	if !app.Autos.loaded && !app.Autos.loading {
		app.loadAutomations()
	}
	ui.Column(c).Width(300).Shrink(0).FillHeight().Background(widgets.Side).BorderWidth(0, 0, 0, 1).BorderColor(widgets.Line).Children(func() {
		ui.Scroll(c.Key("scroll")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(28, 20, 28, 20).Gap(24).Children(func() {
				ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					botAvatar(c, b, 64)
					ui.Text(c, b.Name).FontSize(17).FontWeight(700)
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						widgets.AgentIcon(c, b.Agent, 12)
						ui.Text(c, model.AgentName(b.Agent)).FontSize(12).TextColor(widgets.Muted)
					})
					if b.Note != "" {
						ui.Text(c, b.Note).FontSize(12).TextColor(widgets.Muted).MaxLines(6).Margin(4, 0, 0, 0)
					}
					if ui.Button(c.Key("edit"), tr("Edit Bot")).Margin(4, 0, 0, 0).Clicked() {
						app.editBot(b)
					}
				})
				profileSection(c.Key("duties"), tr("Duties"), "", func() {
					duties := 0
					for _, v := range app.Autos.list {
						if v.Bot != b.ID {
							continue
						}
						duties++
						app.botDuty(c.Key("duty:"+v.ID), v)
					}
					if duties == 0 {
						ui.Text(c, tr("No duties yet. Give it an automation and it works on its own.")).FontSize(12).TextColor(widgets.Muted)
					}
					add := widgets.Quiet(c.Key("add-duty")).Height(26).PaddingX(0).Gap(5).Label(tr("Add a duty"))
					add.Children(func() {
						widgets.Icon(c, "plus", 13, widgets.Accent)
						ui.Text(c, tr("Add a duty")).FontSize(12).TextColor(widgets.Accent)
					})
					if add.Clicked() {
						app.openDutyForm(b)
					}
				})
				active := []memory.Entry{}
				for _, e := range app.Bots.memories {
					if e.State == memory.EntryActive {
						active = append(active, e)
					}
				}
				profileSection(c.Key("memory"), tr("Memory"), fmt.Sprintf(tr("%d of %d"), len(active), memory.MaxBotEntries), func() {
					if len(active) == 0 {
						ui.Text(c, tr("Nothing yet. Tell it what to remember about this work.")).FontSize(12).TextColor(widgets.Muted)
					}
					for _, e := range active {
						app.botMemory(c.Key("memory:"+e.ID), b, e)
					}
				})
				profileSection(c.Key("projects"), tr("Projects"), "", func() {
					labels, paths := app.botProjects(b.Project)
					for _, p := range b.AllProjects() {
						label := p
						for i, q := range paths {
							if q == p {
								label = labels[i]
							}
						}
						ui.Row(c.Key("project:" + p)).Gap(8).AlignItems(ui.Center).Children(func() {
							widgets.Icon(c, "folder", 13, widgets.Muted)
							ui.Text(c, label).FontSize(12).SingleLine().Ellipsis("…").Tooltip(p)
						})
					}
				})
			})
		})
	})
}

// profileSection is a titled group of a Bot's profile.
func profileSection(c *ui.Context, title, detail string, body func()) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(widgets.Muted).Grow(1)
			if detail != "" {
				ui.Text(c, detail).FontSize(11).TextColor(widgets.Faint)
			}
		})
		body()
	})
}

// botDuty is an automation given to the Bot, opening its details.
func (app *App) botDuty(c *ui.Context, v automation.View) {
	row := widgets.Quiet(c).FillWidth().Height(0).MinHeight(44).Radius(10).Background(widgets.Canvas).Padding(7, 10, 7, 10).Gap(9).AlignItems(ui.Center).Label(v.Name)
	row.Children(func() {
		widgets.Icon(c, "clock", 14, widgets.Accent)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, v.Name).FontSize(12).FontWeight(600).SingleLine().Ellipsis("…")
			if v.ScheduleText != "" {
				ui.Text(c, v.ScheduleText).FontSize(11).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
			}
		})
		color := widgets.Faint
		if v.Enabled {
			color = widgets.Success
		}
		ui.Box(c).Size(7, 7).Radius(3.5).Background(color).Shrink(0)
	})
	if row.Clicked() {
		app.ShowAutomations()
		app.Autos.Page, app.Autos.Detail = "detail", v.ID
	}
}

// botMemory is one thing the Bot remembers, which the person can forget.
func (app *App) botMemory(c *ui.Context, b bot.Bot, e memory.Entry) {
	ui.Row(c).FillWidth().Radius(10).Background(widgets.Canvas).Padding(8, 4, 8, 10).Gap(6).AlignItems(ui.Start).Children(func() {
		ui.Text(c, e.Content).FontSize(12).Grow(1).MinWidth(0).MaxLines(4)
		if widgets.ToolButton(c.Key("forget"), "x", tr("Forget")).Size(22, 22).Clicked() {
			app.memoryCall("memoryForget", manager.Request{Project: b.Project, Scope: "bot", Bot: b.ID, ID: e.ID, Version: e.Version}, func(_ json.RawMessage, err error) {
				if err != nil {
					app.C.State().SetStatus(err.Error())
				}
				app.Bots.memFor = ""
			})
		}
	})
}

// loadBotMemory loads what a Bot remembers, for its profile.
func (app *App) loadBotMemory(b bot.Bot) {
	app.Bots.memLoading, app.Bots.memFor = true, b.ID
	app.memoryCall("memoryList", manager.Request{Project: b.Project, Scope: "bot", Bot: b.ID}, func(raw json.RawMessage, err error) {
		app.Bots.memLoading = false
		var list manager.List
		if err == nil {
			_ = json.Unmarshal(raw, &list)
		}
		app.Bots.memories = list.Entries
	})
}

// openDutyForm opens the form of a new automation in the Bot's project, with
// its agent, given to it once saved.
func (app *App) openDutyForm(b bot.Bot) {
	app.ShowAutomations()
	app.OpenAutomationForm(nil)
	app.Autos.project, app.Autos.Agent, app.Autos.forBot = b.Project, b.Agent, b.ID
}
