package nsmobile

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

func (m *App) home(cx *core.Scope) core.Node {
	return core.View{TestID: "mobile-home", Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		m.toolbar(func() string { return "Miya" }, false),
		m.errorText(cx),
		m.button("desktop-manager", tr("Desktops"), nil, func() { m.sheet.Set("desktops") }),
		nswidgets.MutedText(cx, live(m, func() string { return textutil.FirstNonEmpty(m.ConnectionLine(), m.Host, tr("Connected")) })),
		base.Input{Label: tr("Search conversations"), Placeholder: tr("Search conversations"), Value: m.search.Get, OnChange: m.search.Set, TestID: "home-search", Style: core.Style{MinHeight: core.Pt(44), Padding: core.All(12), FontSize: 16}},
		scroll(
			m.button("new-conversation", tr("New conversation"), live(m, func() bool { return len(m.State.Projects) == 0 }), func() { m.StartConversation(m.SelectedProject().Path, m.Agent()); m.Path = "/thread" }),
			core.Show{When: live(m, func() bool { return len(m.State.Projects) == 0 }), Then: func(cx *core.Scope) core.Node { return text(tr("No projects on this desktop yet")) }},
			core.Dynamic(func(cx *core.Scope) core.Node {
				// Activity is derived from the same framework-free presentation rules
				// as the desktop, including unread completion versions.
				return m.activity(cx)
			}),
			core.Each(live(m, func() []model.ProjectSnapshot { return m.State.Projects }), func(p model.ProjectSnapshot) string { return p.Path }, func(cx *core.Scope, p func() model.ProjectSnapshot, _ func() int) core.Node {
				return m.projectRow(cx, p)
			}),
			m.button("automations", tr("Automations"), nil, func() { m.sheet.Set("automations") }),
			m.button("memory", tr("Shared memory"), nil, func() { m.sheet.Set("memory") }),
			m.button("quota", tr("Usage"), nil, func() { m.sheet.Set("quota") }),
		),
	}}
}
func (m *App) activity(cx *core.Scope) core.Node {
	m.revision.Get()
	running, completed := m.SidebarActivityThreads()
	var nodes []core.Node
	for _, t := range append(running, completed...) {
		nodes = append(nodes, m.threadRow(cx, func() model.Thread { return t }, "activity-"))
	}
	if len(nodes) == 0 {
		return core.View{}
	}
	return column(append([]core.Node{text(tr("Activity"))}, nodes...)...)
}
func (m *App) projectRow(cx *core.Scope, p func() model.ProjectSnapshot) core.Node {
	path := core.Untracked(cx, func() string { return p().Path })
	return column(
		core.View{TestID: "project-" + path, Role: core.RoleButton, Label: tr("Project ") + core.Untracked(cx, func() string { return p().Name }),
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(48), Padding: core.All(8)},
			OnPress: func(core.PressEvent) {
				m.act(func() {
					m.Selected = path
					if m.ExpandedProject == path {
						m.ExpandedProject = ""
					} else {
						m.ExpandedProject = path
					}
				})()
			},
			Children: []core.Node{nswidgets.Icon("folder", 20, nil), core.Text{ContentFunc: func() string { return p().Name }, Style: core.Style{FontSize: 18, FontWeight: 600}}}},
		core.Show{When: live(m, func() bool { return m.ExpandedProject == path || strings.TrimSpace(m.search.Get()) != "" }), Then: func(cx *core.Scope) core.Node {
			return column(
				core.Each(func() []model.Thread {
					m.revision.Get()
					q := strings.ToLower(strings.TrimSpace(m.search.Get()))
					threads := p().Threads
					if q != "" {
						var found []model.Thread
						for _, t := range threads {
							if strings.Contains(strings.ToLower(t.Title), q) {
								found = append(found, t)
							}
						}
						return found
					}
					return msgfmt.DisplayedThreads(threads, m.ThreadListExpanded[path])
				}, func(t model.Thread) int { return t.ID }, func(cx *core.Scope, t func() model.Thread, _ func() int) core.Node {
					return m.threadRow(cx, t, "thread-")
				}),
				core.Show{When: live(m, func() bool { return len(p().Threads) > msgfmt.CollapsedThreadCount }), Then: func(cx *core.Scope) core.Node {
					return m.button("more-"+path, tr("Expand"), nil, func() { m.ThreadListExpanded[path] = !m.ThreadListExpanded[path] })
				}},
				m.button("new-"+path, tr("New conversation"), nil, func() { m.StartConversation(path, m.Agent()); m.Path = "/thread" }),
			)
		}},
	)
}
func (m *App) threadRow(cx *core.Scope, t func() model.Thread, prefix string) core.Node {
	id := core.Untracked(cx, func() int { return t().ID })
	return core.View{TestID: prefix + strconv.Itoa(id), Role: core.RoleButton, Label: core.Untracked(cx, func() string { return textutil.FirstNonEmpty(t().Title, tr("Untitled conversation")) }),
		Style:   core.Style{MinHeight: core.Pt(48), Direction: core.Row, AlignItems: core.AlignCenter, Padding: core.All(8), Gap: 10},
		OnPress: func(core.PressEvent) { m.act(func() { m.OpenThread(t()) })() }, Children: []core.Node{
			nswidgets.AgentIcon(core.Untracked(cx, func() string { return model.ThreadAgent(t()) }), 20),
			core.Text{ContentFunc: func() string {
				return msgfmt.NativeText(textutil.FirstNonEmpty(t().Title, tr("Untitled conversation")))
			}, MaxLines: 2, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 16}},
			core.Text{ContentFunc: live(m, func() string {
				if t().Busy {
					return tr("Running")
				}
				if m.CompletionUnread(t()) {
					return tr("Unread")
				}
				return ""
			}), Style: core.Style{FontSize: 13}},
		}}
}
func (m *App) pairView(cx *core.Scope) core.Node {
	return scroll(text(tr("Connect to your desktop")), text(tr("Scan on your phone, or paste a desktop connection code")),
		// TODO(platform): NSGo has no QR scanner; connection-code entry works.
		base.Input{Label: tr("Desktop connection code"), Placeholder: tr("Or paste a connection code"), Value: m.pairCode.Get, OnChange: m.pairCode.Set, TestID: "pair-code", NoAutoCorrect: true, AutoCapitalize: core.AutoCapitalizeNone, Style: core.Style{MinHeight: core.Pt(48), FontSize: 16, Padding: core.All(12)}},
		m.button("paste-code", tr("Paste"), nil, func() { cx.ReadClipboard(func(value string) { m.pairCode.Set(value) }) }),
		m.button("pair-connect", tr("Connect"), nil, func() {
			m.Connect(strings.TrimSpace(m.pairCode.Get()))
			if m.Link != "" {
				m.sheet.Set("")
				m.pairCode.Set("")
			}
		}),
		core.Text{TestID: "pair-error", ContentFunc: live(m, func() string { return m.Error + "\n" + m.PersistenceError }), Style: core.Style{FontSize: 14}},
	)
}
func (m *App) quotas(cx *core.Scope) core.Node {
	return scroll(text(tr("Usage")), core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		var nodes []core.Node
		for _, q := range m.State.Quotas {
			nodes = append(nodes, text(m.agentName(q.Agent)))
			for _, w := range q.Windows {
				nodes = append(nodes, text(fmt.Sprintf(tr("%.0f%% used"), w.UsedPercent)+" · "+tr(w.Label)+" · "+quotaResetLabel(w.ResetsAt)))
			}
		}
		return column(nodes...)
	}))
}
func (m *App) agentName(id string) string {
	for _, d := range m.AgentDescriptors() {
		if d.ID == id {
			return d.Name
		}
	}
	return id
}

func (m *App) projects(cx *core.Scope) core.Node {
	return scroll(text(tr("Select project")), core.Each(live(m, func() []model.ProjectSnapshot { return m.State.Projects }), func(p model.ProjectSnapshot) string { return p.Path }, func(cx *core.Scope, p func() model.ProjectSnapshot, _ func() int) core.Node {
		return m.button("select-"+p().Path, p().Name, nil, func() {
			m.Selected = p().Path
			if m.NewProject != "" {
				m.NewProject = p().Path
			}
			m.sheet.Set("")
		})
	}))
}
