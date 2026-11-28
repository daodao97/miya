package nsmobile

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/automation"
	"miya/internal/chat/model"
)

func (m *App) automationsSheet(cx *core.Scope, page string) core.Node {
	if page == "automation-new" {
		return m.automationCreateSheet(cx)
	}
	m.pollSheet(cx, page, func() {
		if time.Since(m.Autos.RequestedAt) >= 5*time.Second {
			m.LoadAutomations()
		}
	})
	status := live(m, func() string {
		switch {
		case !m.Connected && m.Autos.Loaded:
			return tr("Disconnected. Showing the last synced content.")
		case !m.Connected:
			return tr("Connect to your desktop to view automations.")
		case !m.State.Automations:
			return tr("Update to view automations. If already updated, try again shortly.")
		case m.Autos.Err != "":
			return m.Autos.Err
		case m.Autos.Loading && !m.Autos.Loaded:
			return tr("Loading…")
		case len(m.Autos.List) == 0 && page != "automation":
			return tr("No automations yet")
		}
		return ""
	})
	nodes := []core.Node{
		groupedState(cx, "clock", status, live(m, func() bool { return m.Autos.Loading && m.Connected }),
			live(m, func() bool { return m.Autos.Err != "" && m.Connected })),
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			if page == "automation" {
				for _, v := range m.Autos.List {
					if v.ID == m.Autos.Detail {
						return m.automationDetail(cx, v)
					}
				}
				if m.Autos.Loaded {
					return groupedState(cx, "clock", func() string { return tr("This automation was deleted") }, nil, nil)
				}
				return core.Group(nil)
			}
			return m.automationList(cx)
		}),
	}
	if page != "automation" {
		nodes = append(nodes, m.briefingControls(cx))
	}
	return m.groupedPage(cx, nodes...)
}

func (m *App) automationList(cx *core.Scope) core.Node {
	var content []core.Node
	var rows []gRow
	for _, v := range m.Autos.List {
		rows = append(rows, gRow{ID: "automation-" + v.ID, Label: v.Name, Icon: "clock", Chevron: true,
			Detail: filepath.Base(v.Project) + " · " + automationWhen(v), Value: func() string { return automationState(v) },
			Press: func() { m.Autos.Detail = v.ID; m.pages.Open("automation") }})
	}
	if len(rows) > 0 {
		content = append(content, m.group(cx, "", nil, rows...))
	}
	var attention []gRow
	for _, item := range automation.NeedsAttention(m.Autos.List) {
		attention = append(attention, gRow{Label: item.Name + " · " + automation.AttentionLabel(item.Kind), Detail: item.Detail, Icon: "alert-circle",
			Chevron: true, Press: func() { m.Autos.Detail = item.ID; m.pages.Open("automation") }})
		if !item.Run.IsZero() && m.State.AutomationReview {
			attention = append(attention, gRow{ID: "review-handled-" + item.ID, Label: tr("Mark handled"), Action: true,
				Disabled: live(m, func() bool { return !m.Connected }), Press: func() {
					m.ReviewAutomation(automation.Request{ID: item.ID, Action: "handled", Started: item.Run})
				}})
		}
	}
	if len(attention) > 0 {
		content = append([]core.Node{m.group(cx, tr("Needs your attention"), nil, attention...)}, content...)
	}
	if w := m.Autos.Webhook; w != nil {
		rows := []gRow{{Label: w.Status(), Icon: "webhook"}}
		if w.URL != "" {
			rows = append(rows, gRow{ID: "webhook-copy", Label: tr("Copy webhook URL"), Icon: "copy", Action: true,
				Detail: w.URL, Press: func() { cx.SetClipboard(w.URL) }})
		}
		for _, msg := range w.Messages {
			rows = append(rows, gRow{Label: msg.At.Local().Format("01-02 15:04"), Detail: msg.Data + "\n" + msg.Detail})
			for _, route := range msg.Routes {
				rows = append(rows, gRow{Label: route.Name, Detail: route.Outcome + " · " + route.Detail})
			}
		}
		content = append(content, m.group(cx, tr("Miya webhook"), nil, rows...))
	}
	return core.View{Style: core.Style{Gap: 24}, Children: content}
}

func automationState(v automation.View) string {
	if v.Running {
		return tr("Running")
	}
	if v.Enabled {
		return tr("Enabled")
	}
	return tr("Paused")
}

func automationWhen(v automation.View) string {
	if v.ScheduleText != "" {
		return v.ScheduleText
	}
	return automation.DescribeCron(v.Schedule)
}
func (m *App) automationDetail(cx *core.Scope, v automation.View) core.Node {
	disabled := live(m, func() bool { return !m.Connected || !m.State.AutomationRun || v.Running || m.Autos.RunRequest != "" })
	runStatus := live(m, func() string {
		if !m.State.AutomationRun {
			return tr("Update the desktop app to run now")
		}
		if m.Autos.RunID == v.ID {
			return m.Autos.RunError
		}
		return ""
	})
	details := []gRow{{Label: v.Name, Detail: model.AgentName(v.Agent) + " · " + v.Project,
		Icon: "clock", Value: func() string { return automationState(v) }},
		{Label: tr("Schedule"), Detail: automationWhen(v)},
	}
	if !v.Next.IsZero() {
		details = append(details, gRow{Label: tr("Next run"), Value: func() string { return v.Next.Local().Format("2006-01-02 15:04") }})
	}
	details = append(details, gRow{ID: "automation-run", Label: tr("Run now"), Icon: "player-play", Action: true,
		Disabled: disabled, Press: func() { m.RunAutomation(v.ID) }})
	nodes := []core.Node{
		m.group(cx, "", groupedFooter(cx, "", runStatus), details...),
		groupedSection(cx, tr("Task instructions"), nil, automationBody(v.Instructions)),
	}
	if v.Check != "" {
		nodes = append(nodes, groupedSection(cx, fmt.Sprintf(tr("Completion criteria · Up to %d turns"), v.Rounds), nil, automationBody(v.Check)))
	}
	nodes = append(nodes, m.reviewControls(cx, v)...)
	var runs []core.Node
	if len(v.Runs) == 0 {
		runs = append(runs, automationBody(tr("No runs yet")))
	}
	for i := len(v.Runs) - 1; i >= 0; i-- {
		r := v.Runs[i]
		var lines []string
		for _, s := range []string{automation.WorktreeLabel(r), r.Summary} {
			if s != "" {
				lines = append(lines, s)
			}
		}
		if report := r.Report; report != nil {
			if report.Summary != "" {
				lines = append(lines, report.Summary)
			}
			lines = append(lines, report.Findings...)
			for _, metric := range report.Metrics {
				lines = append(lines, metric.Text())
			}
			lines = append(lines, report.Artifacts...)
			if report.NeedsUser {
				lines = append(lines, tr("Needs your attention"))
			}
		}
		if len(runs) > 0 {
			runs = append(runs, groupedDivider(cx))
		}
		date := ""
		if !r.Started.IsZero() {
			date = r.Started.Local().Format("2006-01-02 15:04")
		}
		runs = append(runs, m.groupedRow(cx, gRow{Label: automation.ResultLabel(r.Result), Icon: "clock", Detail: date}))
		if len(lines) > 0 {
			runs = append(runs, automationBody(strings.Join(lines, "\n")))
		}
		if t, ok := m.AutomationRunThread(v, r); ok {
			runs = append(runs, m.groupedRow(cx, gRow{ID: "automation-conversation-" + strconv.Itoa(i), Label: tr("Open run conversation"),
				Chevron: true, Action: true, Press: func() { m.OpenThread(t); m.pages.Open("") }}))
		} else if r.Thread != 0 || r.ServerID != "" {
			runs = append(runs, groupedFooter(cx, "", func() string { return tr("Conversation unavailable") }))
		}
	}
	nodes = append(nodes, groupedSection(cx, tr("Run history"), nil, runs...))
	return core.View{Style: core.Style{Gap: 24, MinWidth: core.Pt(0)}, Children: nodes}
}

func automationBody(copy string) core.Node {
	return core.Text{Content: copy, Selectable: core.SelectableOn,
		Style: core.Style{FontSize: 15, LineHeight: 22, Padding: core.Symmetric(12, 16), MinWidth: core.Pt(0)}}
}

func (m *App) field(cx *core.Scope, id, label string, get func() string, set func(string), multiline bool, disabled func() bool) core.Node {
	return groupedSection(cx, label, nil, base.Input{TestID: id, Label: label, Value: get,
		OnChange: func(v string) { set(v); m.change() }, Multiline: multiline, Disabled: disabled,
		Style: core.Style{MinHeight: core.Pt(52), MaxHeight: core.Pt(200), Padding: core.Symmetric(12, 16), FontSize: 16}})
}
func (m *App) automationCreateSheet(cx *core.Scope) core.Node {
	f := &m.Autos.Form
	m.pollSheet(cx, "automation-new", func() {
		if f.Project == "" && m.Autos.Detail != "" {
			m.pages.Open("automation")
		}
	})
	locked := live(m, func() bool { return f.Pending != "" || f.CreateKey != "" })
	blocked := live(m, func() bool { return locked() || !m.Connected || !m.State.AutomationCreate })
	invalidate := func() { f.Draft = nil; f.Error = "" }
	nodes := []core.Node{groupedFooter(cx, "", func() string {
		return tr("Describe what to do and when. Your desktop will run it on schedule after saving.")
	}),
		m.field(cx, "automation-request", tr("What to do"), live(m, func() string { return f.Request }), func(v string) { f.Request = v; invalidate() }, true, locked),
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			var projects, agents []gRow
			for _, p := range m.State.Projects {
				projects = append(projects, gRow{ID: "automation-project-" + p.Path, Label: p.Name, Icon: "folder",
					Checked: live(m, func() bool { return f.Project == p.Path }), Disabled: locked,
					Press: func() { f.Project = p.Path; invalidate() }})
			}
			for _, d := range m.AgentDescriptors() {
				agents = append(agents, gRow{ID: "automation-agent-" + d.ID, Label: d.Name,
					Checked: live(m, func() bool { return f.Agent == d.ID }), Disabled: locked,
					Press: func() { f.Agent = d.ID; invalidate() }})
			}
			return core.View{Style: core.Style{Gap: 16}, Children: []core.Node{
				m.group(cx, tr("Project"), nil, projects...), m.group(cx, tr("Agent"), nil, agents...),
			}}
		}),
		ui.Button{Text: tr("Generate settings"), TestID: "automation-generate", Variant: ui.ButtonSecondary, Size: ui.SizeLarge,
			Disabled: live(m, func() bool { return blocked() || strings.TrimSpace(f.Request) == "" }),
			OnPress:  m.act(func() { m.SendAutomationForm("automation-draft") })},
	}
	draft := core.NewMemo(cx, live(m, func() *automation.View { return f.Draft }))
	nodes = append(nodes, core.Dynamic(func(cx *core.Scope) core.Node {
		d := draft.Get()
		if d == nil {
			return core.View{}
		}
		field := func(id, label string, value *string, multi bool) core.Node {
			return m.field(cx, "automation-"+id, tr(label), live(m, func() string { return *value }), func(v string) {
				*value = v
				if id == "schedule" {
					d.TimeMissing = false
				}
			}, multi, locked)
		}
		content := []core.Node{text(tr("Generated settings"))}
		content = append(content, core.Show{When: live(m, func() bool { return d.TimeMissing }), Then: func(*core.Scope) core.Node {
			return text(tr("No time specified. Using 9:00 AM daily; you can change it below."))
		}})
		content = append(content, field("name", "Name", &d.Name, false), field("schedule", "Schedule", &d.Schedule, false), text(tr("Format: minute hour day month weekday. For example, 0 9 * * 1-5 runs at 9 AM on weekdays.")),
			core.Text{ContentFunc: live(m, func() string { return automation.DescribeCron(d.Schedule) + tr(" · Desktop local time") })},
			text(tr("Wakes: ")+automation.WakeText(d.Automation, nil)+" · "+automation.PolicyText(d.Policy)), field("instructions", "Task instructions", &d.Instructions, true), field("check", "Check command (optional)", &d.Check, false))
		var rounds []gRow
		for _, n := range []int{1, 2, 3, 4, 5} {
			rounds = append(rounds, gRow{ID: "automation-rounds-" + strconv.Itoa(n), Label: fmt.Sprintf(tr("Up to %d turns"), n),
				Disabled: locked, Checked: live(m, func() bool { return d.Rounds == n }), Press: func() { d.Rounds = n }})
		}
		toggle := func(id, label string, get func() bool, set func(bool)) gRow {
			label = tr(label)
			return gRow{Label: label, Switch: &ui.Switch{TestID: id, Label: label, Checked: live(m, get), Disabled: locked,
				OnChange: func(on bool) { m.act(func() { set(on) })() }}}
		}
		content = append(content, m.group(cx, "", nil, rounds...),
			m.group(cx, "", nil,
				toggle("automation-enabled", "Enable after saving", func() bool { return f.Enabled }, func(v bool) { f.Enabled = v }),
				toggle("automation-carry", "Include previous result", func() bool { return !d.NoCarryOver }, func(v bool) { d.NoCarryOver = !v }),
				toggle("automation-worktree", "Run in a separate worktree", func() bool { return d.Worktree }, func(v bool) { d.Worktree = v })))
		return core.View{Style: core.Style{Gap: 16}, Children: content}
	}), core.Text{ContentFunc: live(m, func() string {
		if !m.Connected {
			return tr("Connect to your desktop to continue creating")
		}
		if !m.State.AutomationCreate {
			return tr("Update the desktop app to create automations")
		}
		if f.Pending != "" {
			if f.Op == "automation-draft" {
				return tr("Generating…")
			}
			return tr("Saving…")
		}
		return f.Error
	})},
		ui.Button{Text: tr("Create"), TestID: "automation-create", Variant: ui.ButtonPrimary, Size: ui.SizeLarge,
			Disabled: live(m, func() bool { return f.Draft == nil || f.Pending != "" || !m.Connected || !m.State.AutomationCreate }),
			OnPress:  m.act(func() { m.SendAutomationForm("automation-create") })})
	return m.groupedPage(cx, nodes...)
}

func (m *App) briefingControls(cx *core.Scope) core.Node {
	timeText := core.NewSignal(cx, "")
	var last *automation.BriefingView
	cx.Effect(func() {
		m.revision.Get()
		if next := m.Autos.Briefing; next != nil && next != last {
			if last == nil || next.Time != last.Time || timeText.GetUntracked() == last.Time {
				timeText.Set(next.Time)
			}
			last = next
		}
	})
	disabled := live(m, func() bool { return !m.Connected })
	return core.Show{When: live(m, func() bool { return m.Autos.Briefing != nil && m.State.AutomationBriefing }), Then: func(cx *core.Scope) core.Node {
		return groupedSection(cx, tr("Daily briefing"), nil,
			base.Input{TestID: "automation-briefing-time", Label: tr("Briefing time"), Placeholder: "09:00", Value: timeText.Get,
				OnChange: timeText.Set, Disabled: disabled, Style: core.Style{Padding: core.Symmetric(12, 16), MinHeight: core.Pt(52)}},
			groupedDivider(cx),
			m.groupedRow(cx, gRow{ID: "automation-briefing-save", Label: tr("Save"), Action: true,
				Disabled: disabled, Press: func() { m.SetAutomationBriefing(timeText.Get()) }}),
			m.groupedRow(cx, gRow{ID: "automation-briefing-off", Label: tr("Off"), Disabled: disabled, Press: func() { m.SetAutomationBriefing("") }}))
	}}
}

func (m *App) reviewControls(cx *core.Scope, v automation.View) []core.Node {
	if !m.State.AutomationReview {
		return nil
	}
	disabled := live(m, func() bool { return !m.Connected })
	action := func(id, label, review, op, scope string) gRow {
		return gRow{ID: id, Label: label, Disabled: disabled, Action: true, Destructive: op == "deny" || op == "revoke",
			Press: func() { m.ReviewAutomation(automation.Request{Review: review, Action: op, Scope: scope}) }}
	}
	var nodes []core.Node
	for _, r := range v.Reviews {
		rows := []gRow{{Label: r.Action, Detail: r.Dir, Icon: "shield-exclamation"},
			action("review-once-"+r.ID, tr("Approve once"), r.ID, "approve", automation.ScopeOnce)}
		if r.Key != "" && r.Key != r.Action {
			rows = append(rows, action("review-kind-"+r.ID, fmt.Sprintf(tr("Approve all %s for 7 days"), r.Key), r.ID, "approve", automation.ScopeKind))
			if r.Project != "" {
				rows = append(rows, action("review-project-"+r.ID, fmt.Sprintf(tr("Approve all %s in this project for 7 days"), r.Key), r.ID, "approve", automation.ScopeProject))
			}
		}
		rows = append(rows, action("review-deny-"+r.ID, tr("Deny"), r.ID, "deny", ""))
		nodes = append(nodes, m.group(cx, tr("Waiting for your review"), nil, rows...))
	}
	for _, grant := range v.Grants {
		nodes = append(nodes, m.group(cx, "", nil, gRow{Label: grant.Action, Detail: automation.FormatTime(grant.Expires), Icon: "shield-check"},
			action("review-revoke-"+grant.Review, tr("Revoke"), grant.Review, "revoke", "")))
	}
	return nodes
}
