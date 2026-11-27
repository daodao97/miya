package nsui

import (
	"slices"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func botsPage(cx *core.Scope, c Host, showChat func()) core.Node {
	editing := core.NewSignal[*bot.Bot](cx, nil)
	pending := core.NewSignal(cx, false)
	message := core.NewSignal(cx, "")
	call := func(op string, req bot.Request, done func()) {
		if pending.Get() {
			return
		}
		pending.Set(true)
		message.Set("")
		finish := func(err error) {
			pending.Set(false)
			if err != nil {
				message.Set(err.Error())
				return
			}
			done()
		}
		if c.State().Remote == nil {
			_, err := c.BotOp(op, req)
			finish(err)
			return
		}
		go func() {
			_, err := c.BotOp(op, req)
			cx.Post(func() { finish(err) })
		}()
	}
	talk := func(b bot.Bot) {
		if c.State().Cwd != b.Project {
			if err := c.ChangeWorkspace(b.Project); err != nil {
				message.Set(err.Error())
				return
			}
		}
		if t := c.BotThread(b); t.ID != 0 {
			c.SelectThread(t)
		} else {
			c.NewThread()
			c.SetDraftBot(b.ID)
		}
		showChat()
		if len(b.Reports) > 0 {
			call("botRead", bot.Request{ID: b.ID}, func() {})
		}
	}
	colors := nswidgets.ColorsOf(cx)
	return core.Scroll{TestID: "bots-page", Style: ui.FillRest, Children: []core.Node{
		core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(920), AlignSelf: core.AlignCenter, Padding: core.All(24), Gap: 16}, Children: []core.Node{
			autoRow(autoHeading(tr("Bots")), autoButton("bot-new", "New Bot", func() {
				editing.Set(&bot.Bot{Agent: c.CurrentAgent(), Project: c.State().Cwd})
			})),
			core.Text{TestID: "bots-error", ContentFunc: message.Get, StyleFunc: func() core.Style { return core.Style{Color: colors.Danger()} }},
			core.Dynamic(func(cx *core.Scope) core.Node {
				if draft := editing.Get(); draft != nil {
					return botForm(cx, c, *draft, pending.Get, func(b bot.Bot) {
						call("botSave", bot.Request{Bot: b}, func() { editing.Set(nil) })
					}, func() { editing.Set(nil) })
				}
				var nodes []core.Node
				for _, b := range Live(c.Bots)() {
					row := []core.Node{
						autoRow(nswidgets.AgentIcon(b.Agent, 20), autoHeading(b.Name)),
						autoMuted(cx, model.AgentName(b.Agent)+" · "+b.Project),
					}
					if b.Preview != "" {
						row = append(row, autoText(b.Preview))
					}
					for _, report := range b.Reports {
						row = append(row, autoRow(autoMuted(cx, report.Title+" · "+report.Summary),
							autoButton("bot-report-"+report.Session, "Open conversation", func() {
								if t := c.BotReportThread(report); t.ID != 0 {
									c.SelectThread(t)
								} else {
									message.Set(tr("This conversation is not loaded yet."))
								}
							})))
					}
					confirm := core.NewSignal(cx, "")
					row = append(row, autoRow(
						autoButton("bot-open-"+b.ID, "Open conversation", func() { talk(b) }),
						autoButton("bot-edit-"+b.ID, "Edit", func() { editing.Set(&b) }),
						autoButton("bot-reset-"+b.ID, "New conversation", func() { confirm.Set("botReset") }),
						autoButton("bot-delete-"+b.ID, "Delete", func() { confirm.Set("botDelete") }),
					), core.Show{When: func() bool { return confirm.Get() != "" }, Then: func(cx *core.Scope) core.Node {
						return autoRow(
							autoButton("bot-confirm-"+b.ID, "Confirm", func() { call(confirm.Get(), bot.Request{ID: b.ID}, func() { confirm.Set("") }) }),
							autoButton("bot-cancel-"+b.ID, "Cancel", func() { confirm.Set("") }),
						)
					}})
					nodes = append(nodes, autoCard(cx, row...))
				}
				if len(nodes) == 0 {
					return autoMuted(cx, tr("No Bots yet"))
				}
				return autoColumn(nodes...)
			}),
		}},
	}}
}

func botForm(cx *core.Scope, c Host, b bot.Bot, busy func() bool, save func(bot.Bot), cancel func()) core.Node {
	name, note := core.NewSignal(cx, b.Name), core.NewSignal(cx, b.Note)
	agent, modelID, effort := core.NewSignal(cx, b.Agent), core.NewSignal(cx, b.Model), core.NewSignal(cx, b.Effort)
	project, reviewer := core.NewSignal(cx, b.Project), core.NewSignal(cx, b.Reviewer)
	access := core.NewSignal(cx, automation.Access(b.Access))
	var agents, projects []ui.Option[string]
	reviewers := []ui.Option[string]{{Value: "", Label: tr("None")}}
	for _, id := range model.Backends {
		agents = append(agents, ui.Option[string]{Value: id, Label: model.AgentName(id)})
	}
	seen := map[string]bool{}
	if get := c.State().Projects; get != nil {
		for _, p := range get() {
			projects = append(projects, ui.Option[string]{Value: p.Path, Label: p.Name})
			seen[p.Path] = true
		}
	}
	if !seen[b.Project] && b.Project != "" {
		projects = append(projects, ui.Option[string]{Value: b.Project, Label: b.Project})
	}
	for _, v := range c.Bots() {
		if v.ID != b.ID {
			reviewers = append(reviewers, ui.Option[string]{Value: v.ID, Label: v.Name})
		}
	}
	mounted := core.NewSignalFunc(cx, slices.Clone(b.Projects), slices.Equal[[]string, string])
	var additional []core.Node
	for _, p := range projects {
		additional = append(additional, core.Show{When: func() bool { return p.Value != project.Get() }, Then: func(cx *core.Scope) core.Node {
			return ui.Switch{Label: p.Label, Checked: func() bool { return slices.Contains(mounted.Get(), p.Value) }, OnChange: func(on bool) {
				next := slices.DeleteFunc(slices.Clone(mounted.Get()), func(v string) bool { return v == p.Value })
				if on {
					next = append(next, p.Value)
				}
				mounted.Set(next)
			}}
		}})
	}
	return autoCard(cx,
		base.Input{TestID: "bot-name", Label: tr("Name"), Placeholder: tr("Name"), Value: name.Get, OnChange: name.Set},
		base.Input{TestID: "bot-charter", Label: tr("Charter"), Placeholder: tr("Charter"), Multiline: true, Value: note.Get, OnChange: note.Set},
		ui.Select[string]{Label: tr("Project"), Options: projects, Value: project.Get, OnChange: project.Set},
		ui.Select[string]{Label: tr("Agent"), Options: agents, Value: agent.Get, OnChange: agent.Set},
		base.Input{Label: tr("Model"), Placeholder: tr("Model"), Value: modelID.Get, OnChange: modelID.Set},
		base.Input{Label: tr("Effort"), Placeholder: tr("Effort"), Value: effort.Get, OnChange: effort.Set},
		ui.Select[automation.Access]{Label: tr("Access"), Options: automationAccessOptions(), Value: access.Get, OnChange: access.Set},
		ui.Select[string]{Label: tr("Reviewer"), Options: reviewers, Value: reviewer.Get, OnChange: reviewer.Set},
		autoHeading(tr("Other projects")), autoColumn(additional...),
		autoRow(ui.Button{TestID: "bot-save", Text: tr("Save"), Disabled: busy, OnPress: func() {
			b.Name, b.Note, b.Agent, b.Model, b.Effort = name.Get(), note.Get(), agent.Get(), modelID.Get(), effort.Get()
			b.Project, b.Reviewer, b.Access = project.Get(), reviewer.Get(), string(access.Get())
			b.Projects = slices.DeleteFunc(slices.Clone(mounted.Get()), func(v string) bool { return v == b.Project })
			save(b)
		}}, autoButton("bot-form-cancel", "Cancel", cancel)),
	)
}
