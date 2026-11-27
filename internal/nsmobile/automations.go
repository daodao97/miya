package nsmobile

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func (m *App) automationsSheet(cx *core.Scope, page string) core.Node {
	if page == "automation-new" {
		return m.automationCreateSheet(cx)
	}
	m.pollSheet(cx, func() {
		if time.Since(m.Autos.RequestedAt) >= 5*time.Second {
			m.LoadAutomations()
		}
	})
	nodes := []core.Node{text(tr("Automations")), m.sheetBack("automations-back", tr("Back"), "")}
	if page == "automation" {
		nodes[1] = m.sheetBack("automations-back", tr("Back"), "automations")
	}
	nodes = append(nodes,
		m.button("automations-refresh", tr("Refresh automations"), live(m, func() bool { return !m.Connected || !m.State.Automations || m.Autos.Loading }), m.LoadAutomations),
		m.button("automation-new", tr("New automation"), live(m, func() bool { return !m.Connected || !m.State.AutomationCreate }), func() { m.OpenAutomationCreate(); m.pages.Open("automation-new") }),
		core.Text{ContentFunc: live(m, func() string {
			if !m.Connected {
				if m.Autos.Loaded {
					return tr("Disconnected. Showing the last synced content.")
				}
				return tr("Connect to your desktop to view automations.")
			}
			if !m.State.Automations {
				return tr("Update to view automations. If already updated, try again shortly.")
			}
			if m.Autos.Err != "" {
				return m.Autos.Err
			}
			if m.Autos.Loading {
				return tr("Loading…")
			}
			if len(m.Autos.List) == 0 {
				return tr("No automations yet")
			}
			return ""
		})})
	if page != "automation" {
		nodes = append(nodes, m.briefingControls(cx))
	}
	nodes = append(nodes, core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		var content []core.Node
		if page == "automation" {
			for _, v := range m.Autos.List {
				if v.ID == m.Autos.Detail {
					return m.automationDetail(cx, v)
				}
			}
			if m.Autos.Loaded {
				return text(tr("This automation was deleted"))
			}
			return core.View{}
		}
		if w := m.Autos.Webhook; w != nil {
			content = append(content, text(tr("Miya webhook")+" · "+w.Status()), text(w.URL))
			if w.URL != "" {
				content = append(content, m.button("webhook-copy", tr("Copy webhook URL"), nil, func() { cx.SetClipboard(w.URL) }))
			}
			for _, msg := range w.Messages {
				content = append(content, text(msg.At.Local().Format("01-02 15:04")+" · "+msg.Data+"\n"+msg.Detail))
				for _, route := range msg.Routes {
					content = append(content, text(route.Name+" · "+route.Outcome+" · "+route.Detail))
				}
			}
		}
		for _, item := range automation.NeedsAttention(m.Autos.List) {
			content = append(content, text(item.Name+" · "+automation.AttentionLabel(item.Kind)), text(item.Detail))
			if !item.Run.IsZero() && m.State.AutomationReview {
				content = append(content, m.button("review-handled-"+item.ID, tr("Mark handled"), live(m, func() bool { return !m.Connected }), func() {
					m.ReviewAutomation(automation.Request{ID: item.ID, Action: "handled", Started: item.Run})
				}))
			}
		}
		for _, v := range m.Autos.List {
			content = append(content, m.button("automation-"+v.ID, v.Name+"\n"+filepath.Base(v.Project)+" · "+model.AgentName(v.Agent)+"\n"+automationWhen(v), nil, func() { m.Autos.Detail = v.ID; m.pages.Open("automation") }))
		}
		return column(content...)
	}))
	return scroll(nodes...)
}
func automationWhen(v automation.View) string {
	if v.ScheduleText != "" {
		return v.ScheduleText
	}
	return automation.DescribeCron(v.Schedule)
}
func (m *App) automationDetail(cx *core.Scope, v automation.View) core.Node {
	status := tr("Paused")
	if v.Running {
		status = tr("Running")
	} else if v.Enabled {
		status = tr("Enabled")
	}
	nodes := []core.Node{text(v.Name), text(status + " · " + model.AgentName(v.Agent) + " · " + v.Project), text(automationWhen(v))}
	if !v.Next.IsZero() {
		nodes = append(nodes, text(tr("Next run")+": "+v.Next.Local().Format("2006-01-02 15:04")))
	}
	nodes = append(nodes, m.button("automation-run", tr("Run now"), live(m, func() bool { return !m.Connected || !m.State.AutomationRun || v.Running || m.Autos.RunRequest != "" }), func() { m.RunAutomation(v.ID) }),
		core.Text{ContentFunc: live(m, func() string {
			if !m.State.AutomationRun {
				return tr("Update the desktop app to run now")
			}
			if m.Autos.RunID == v.ID {
				return m.Autos.RunError
			}
			return ""
		})},
		text(tr("Task instructions")), text(v.Instructions))
	if v.Check != "" {
		nodes = append(nodes, text(fmt.Sprintf(tr("Completion criteria · Up to %d turns"), v.Rounds)+"\n"+v.Check))
	}
	nodes = append(nodes, m.reviewControls(v)...)
	nodes = append(nodes, text(tr("Run history")))
	if len(v.Runs) == 0 {
		nodes = append(nodes, text(tr("No runs yet")))
	}
	for i := len(v.Runs) - 1; i >= 0; i-- {
		r := v.Runs[i]
		body := []core.Node{text(r.Started.Local().Format("2006-01-02 15:04") + " · " + automation.ResultLabel(r.Result)), text(automation.WorktreeLabel(r)), text(r.Summary)}
		if report := r.Report; report != nil {
			body = append(body, text(report.Summary))
			for _, f := range report.Findings {
				body = append(body, text(f))
			}
			for _, metric := range report.Metrics {
				body = append(body, text(metric.Text()))
			}
			for _, a := range report.Artifacts {
				body = append(body, text(a))
			}
			if report.NeedsUser {
				body = append(body, text(tr("Needs your attention")))
			}
		}
		if t, ok := m.AutomationRunThread(v, r); ok {
			body = append(body, m.button("automation-conversation-"+strconv.Itoa(i), tr("Open run conversation"), nil, func() { m.OpenThread(t); m.pages.Open("") }))
		} else if r.Thread != 0 || r.ServerID != "" {
			body = append(body, text(tr("Conversation unavailable")))
		}
		nodes = append(nodes, column(body...))
	}
	return column(nodes...)
}

func (m *App) field(id, label string, get func() string, set func(string), multiline bool, disabled func() bool) core.Node {
	return column(text(label), base.Input{TestID: id, Label: label, Value: get, OnChange: func(v string) { set(v); m.change() }, Multiline: multiline, Disabled: disabled, Style: core.Style{MinHeight: core.Pt(44), MaxHeight: core.Pt(200), Padding: core.All(12), FontSize: 16}, StyleFunc: func() core.Style { return core.Style{Background: nswidgets.ColorsOf(m.cx).Bubble()} }})
}
func (m *App) automationCreateSheet(cx *core.Scope) core.Node {
	f := &m.Autos.Form
	m.pollSheet(cx, func() {
		if f.Project == "" && m.Autos.Detail != "" {
			m.pages.Open("automation")
		}
	})
	locked := live(m, func() bool { return f.Pending != "" || f.CreateKey != "" })
	blocked := live(m, func() bool { return locked() || !m.Connected || !m.State.AutomationCreate })
	invalidate := func() { f.Draft = nil; f.Error = "" }
	nodes := []core.Node{text(tr("New automation")), m.sheetBack("automation-create-back", tr("Back"), "automations"), text(tr("Describe what to do and when. Your desktop will run it on schedule after saving.")),
		m.field("automation-request", tr("What to do"), live(m, func() string { return f.Request }), func(v string) { f.Request = v; invalidate() }, true, locked),
		core.Dynamic(func(cx *core.Scope) core.Node {
			m.revision.Get()
			var choices []core.Node
			for _, p := range m.State.Projects {
				label := p.Name
				if f.Project == p.Path {
					label = "✓ " + label
				}
				choices = append(choices, m.button("automation-project-"+p.Path, label, locked, func() { f.Project = p.Path; invalidate() }))
			}
			for _, d := range m.AgentDescriptors() {
				label := d.Name
				if d.ID == f.Agent {
					label = "✓ " + label
				}
				choices = append(choices, m.button("automation-agent-"+d.ID, label, locked, func() { f.Agent = d.ID; invalidate() }))
			}
			return column(choices...)
		}), m.button("automation-generate", tr("Generate settings"), live(m, func() bool { return blocked() || strings.TrimSpace(f.Request) == "" }), func() { m.SendAutomationForm("automation-draft") })}
	draft := core.NewMemo(cx, live(m, func() *automation.View { return f.Draft }))
	nodes = append(nodes, core.Dynamic(func(cx *core.Scope) core.Node {
		d := draft.Get()
		if d == nil {
			return core.View{}
		}
		field := func(id, label string, value *string, multi bool) core.Node {
			return m.field("automation-"+id, tr(label), live(m, func() string { return *value }), func(v string) {
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
		for _, n := range []int{1, 2, 3, 4, 5} {
			label := fmt.Sprintf(tr("Up to %d turns"), n)
			content = append(content, core.View{TestID: "automation-rounds-" + strconv.Itoa(n), Role: core.RoleRadio, Label: label, Checked: live(m, func() bool { return d.Rounds == n }), Disabled: locked, Style: core.Style{MinHeight: core.Pt(44), Padding: core.All(12)}, OnPress: func(core.PressEvent) {
				if !locked() {
					d.Rounds = n
					m.change()
				}
			}, Children: []core.Node{core.Text{ContentFunc: live(m, func() string {
				if d.Rounds == n {
					return "✓ " + label
				}
				return label
			}), Style: core.Style{FontSize: 16}}}})
		}

		toggle := func(id, label string, get func() bool, set func(bool)) core.Node {
			return nsToggle(m, id, tr(label), get, set, locked)
		}
		content = append(content, toggle("automation-enabled", "Enable after saving", func() bool { return f.Enabled }, func(v bool) { f.Enabled = v }), toggle("automation-carry", "Include previous result", func() bool { return !d.NoCarryOver }, func(v bool) { d.NoCarryOver = !v }), toggle("automation-worktree", "Run in a separate worktree", func() bool { return d.Worktree }, func(v bool) { d.Worktree = v }))
		return column(content...)
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
		m.button("automation-create", tr("Create"), live(m, func() bool { return f.Draft == nil || f.Pending != "" || !m.Connected || !m.State.AutomationCreate }), func() { m.SendAutomationForm("automation-create") }))
	return scroll(nodes...)
}
func nsToggle(m *App, id, label string, get func() bool, set func(bool), disabled func() bool) core.Node {
	return core.View{Role: core.RoleSwitch, Label: label, TestID: id, Checked: live(m, get), Disabled: disabled, Style: core.Style{MinHeight: core.Pt(44), Padding: core.All(12)}, OnPress: func(core.PressEvent) {
		if !disabled() {
			set(!get())
			m.change()
		}
	}, Children: []core.Node{core.Text{ContentFunc: live(m, func() string {
		if get() {
			return "✓ " + label
		}
		return label
	}), Style: core.Style{FontSize: 16}}}}
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
		return column(text(tr("Daily briefing")),
			base.Input{TestID: "automation-briefing-time", Label: tr("Briefing time"), Placeholder: "09:00", Value: timeText.Get, OnChange: timeText.Set, Disabled: disabled},
			m.button("automation-briefing-save", tr("Save"), disabled, func() { m.SetAutomationBriefing(timeText.Get()) }),
			m.button("automation-briefing-off", tr("Off"), disabled, func() { m.SetAutomationBriefing("") }),
		)
	}}
}

func (m *App) reviewControls(v automation.View) []core.Node {
	if !m.State.AutomationReview {
		return nil
	}
	disabled := live(m, func() bool { return !m.Connected })
	action := func(review, action, scope string) func() {
		return func() { m.ReviewAutomation(automation.Request{Review: review, Action: action, Scope: scope}) }
	}
	var nodes []core.Node
	for _, r := range v.Reviews {
		nodes = append(nodes, text(tr("Waiting for your review")), text(r.Action), text(r.Dir),
			m.button("review-once-"+r.ID, tr("Approve once"), disabled, action(r.ID, "approve", automation.ScopeOnce)),
			m.button("review-deny-"+r.ID, tr("Deny"), disabled, action(r.ID, "deny", "")),
		)
		if r.Key != "" && r.Key != r.Action {
			nodes = append(nodes, m.button("review-kind-"+r.ID, fmt.Sprintf(tr("Approve all %s for 7 days"), r.Key), disabled, action(r.ID, "approve", automation.ScopeKind)))
			if r.Project != "" {
				nodes = append(nodes, m.button("review-project-"+r.ID, fmt.Sprintf(tr("Approve all %s in this project for 7 days"), r.Key), disabled, action(r.ID, "approve", automation.ScopeProject)))
			}
		}
	}
	for _, grant := range v.Grants {
		nodes = append(nodes, text(grant.Action+" · "+automation.FormatTime(grant.Expires)),
			m.button("review-revoke-"+grant.Review, tr("Revoke"), disabled, action(grant.Review, "revoke", "")))
	}
	return nodes
}
