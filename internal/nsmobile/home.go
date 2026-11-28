package nsmobile

import (
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"

	"miya/internal/nswidgets"
)

// home is the page a new conversation starts from, as the old phone app's:
// the agent, "Let’s build", the project it starts in and the agents to
// choose from, over the composer. The first send opens the conversation.
// The projects and their conversations are in the drawer.
func (m *App) home(cx *core.Scope) core.Node {
	noProjects := core.NewMemo(cx, live(m, func() bool { return len(m.State.Projects) == 0 }))
	connected := core.NewMemo(cx, live(m, func() bool { return m.Connected }))
	state := chat.NewConversationState(cx)
	return core.View{TestID: "mobile-home", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		m.homeHeader(cx),
		core.Show{When: noProjects.Get, Then: func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(24)}, Children: []core.Node{
				core.Show{When: connected.Get, Then: func(cx *core.Scope) core.Node {
					return nswidgets.MutedText(cx, func() string { return tr("No projects on this desktop yet") })
				}, Else: func(cx *core.Scope) core.Node { return ui.Spinner{} }},
			}}
		}},
		core.Show{When: func() bool { return !noProjects.Get() }, Then: func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
				m.homeIntro(cx),
				core.View{Style: core.Style{FlexShrink: 0, Padding: core.Edges{Top: 6, Left: 12, Right: 12, Bottom: 10}}, Children: []core.Node{m.composer(cx, state)}},
			}}
		}},
	}}
}

// homeIntro is the scrolling part of the new-conversation page.
func (m *App) homeIntro(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	agent := core.NewMemo(cx, live(m, m.Agent))
	// The agents change rarely; their pills are built again only then.
	agents := core.NewMemo(cx, live(m, func() string {
		var b strings.Builder
		for _, d := range m.AgentDescriptors() {
			b.WriteString(d.ID + "\x00" + d.Name + "\x01")
		}
		return b.String()
	}))
	return core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100)}, Children: []core.Node{
		core.View{Style: core.Style{Width: core.Pct(100), MinHeight: core.Pt(220), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 14, Padding: core.All(24)}, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node { return nswidgets.AgentIcon(agent.Get(), 46) }),
			core.Text{TestID: "home-title", Content: tr("Let’s build"), Style: core.Style{FontSize: 28, FontWeight: 600}},
			core.View{TestID: "pick-project", Role: core.RoleButton, Label: tr("Select project"),
				OnPress: func(core.PressEvent) { m.act(func() { m.pages.Open("projects") })() },
				Style:   core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, MinHeight: core.Pt(44), MaxWidth: core.Pct(100), MinWidth: core.Pt(0)},
				Children: []core.Node{
					core.Text{TestID: "home-project", ContentFunc: live(m, func() string { return m.SelectedProject().Name }), MaxLines: 1, StyleFunc: func() core.Style {
						return core.Style{FontSize: 24, Color: colors.Muted(), FlexShrink: 1, MinWidth: core.Pt(0)}
					}},
					nswidgets.Icon("chevron-down", 16, colors.Muted),
				}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				agents.Get()
				var pills []core.Node
				for _, d := range core.Untracked(cx, m.AgentDescriptors) {
					pills = append(pills, m.agentPill(cx, d.ID, d.Name, agent.Get))
				}
				return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, JustifyContent: core.JustifyCenter, Gap: 8, Width: core.Pct(100)}, Children: pills}
			}),
		}},
	}}
}

// agentPill chooses the agent a new conversation starts with; the chosen
// one is filled with the ink.
func (m *App) agentPill(cx *core.Scope, id, name string, current func() string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	chosen := func() bool { return current() == id }
	return core.View{TestID: "agent-" + id, Role: core.RoleButton, Label: name,
		OnPress: func(core.PressEvent) { m.act(func() { m.NewAgent = id })() },
		StyleFunc: func() core.Style {
			st := core.Style{MinHeight: core.Pt(44), Padding: core.Symmetric(10, 16), CornerRadius: 22, JustifyContent: core.JustifyCenter, Background: colors.Bubble()}
			if chosen() {
				st.Background = colors.Ink()
			}
			return st
		},
		Children: []core.Node{core.Text{Content: name, MaxLines: 1, StyleFunc: func() core.Style {
			c := colors.Ink()
			if chosen() {
				c = colors.Canvas()
			}
			return core.Style{FontSize: 14, FontWeight: 500, Color: c}
		}}}}
}

func (m *App) pairView(cx *core.Scope, atRoot bool) core.Node {
	var sections []core.Node
	if atRoot {
		sections = append(sections, core.View{Style: core.Style{AlignItems: core.AlignCenter, Gap: 12, Padding: core.Edges{Top: 20, Bottom: 4}}, Children: []core.Node{
			nswidgets.Icon("device-desktop", 36, nswidgets.ColorsOf(cx).Link),
			core.Text{Content: tr("Connect to your desktop"), Role: core.RoleHeader, Style: core.Style{FontSize: 26, FontWeight: 600, TextAlign: core.TextAlignCenter}},
		}})
	}
	paste := func() { cx.ReadClipboard(func(value string) { m.pairCode.Set(value) }) }
	connect := func() {
		cx.DismissKeyboard()
		m.pair(m.pairCode.Get())
	}
	disabled := func() bool { return strings.TrimSpace(m.pairCode.Get()) == "" }
	sections = append(sections,
		m.group(cx, "", groupedFooter(cx, "", func() string { return tr("Scan on your phone, or paste a desktop connection code") }),
			gRow{ID: "pair-scan", Label: tr("Scan to connect"), Icon: "device-mobile", Chevron: true, Disabled: live(m, func() bool { return m.scanning }), Press: m.scan}),
		groupedSection(cx, tr("Connection code"), nil,
			base.Input{Label: tr("Desktop connection code"), Placeholder: tr("Or paste a connection code"), Value: m.pairCode.Get, OnChange: m.pairCode.Set,
				TestID: "pair-code", NoAutoCorrect: true, AutoCapitalize: core.AutoCapitalizeNone,
				OnSubmit: func(string) {
					if !disabled() {
						m.act(connect)()
					}
				},
				Style: core.Style{MinHeight: core.Pt(52), FontSize: 16, Padding: core.Symmetric(12, 16)}},
			m.groupedRow(cx, gRow{ID: "paste-code", Label: tr("Paste"), Icon: "copy", Action: true, Press: paste})),
		ui.Button{Text: tr("Connect"), TestID: "pair-connect", Size: ui.SizeLarge, Variant: ui.ButtonPrimary, Disabled: disabled,
			OnPress: m.act(connect), Style: core.Style{MinHeight: core.Pt(48)}},
		groupedState(cx, "alert-circle", live(m, func() string { return strings.TrimSpace(m.Error + "\n" + m.PersistenceError) }), nil, func() bool { return true }),
	)
	return m.groupedPage(cx, sections...)
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
	return m.groupedPage(cx, core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		if len(m.State.Projects) == 0 {
			return groupedState(cx, "folder", func() string { return tr("No projects on this desktop yet") }, nil, nil)
		}
		var rows []gRow
		for _, p := range m.State.Projects {
			rows = append(rows, gRow{ID: "select-" + p.Path, Label: p.Name, Icon: "folder",
				Checked: live(m, func() bool { return m.SelectedProject().Path == p.Path }),
				Press: func() {
					m.Selected = p.Path
					m.ExpandedProject = p.Path
					if m.NewProject != "" {
						m.NewProject = p.Path
					}
					m.pages.Pop()
				}})
		}
		return m.group(cx, "", nil, rows...)
	}))
}
