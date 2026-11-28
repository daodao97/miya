package nsmobile

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

func (m *App) conversationActions(cx *core.Scope) core.Node {
	close := func() { m.pages.menu.Set(false) }
	return ui.Sheet{
		TestID: "conversation-actions-sheet", Label: tr("Conversation actions"), Open: m.pages.menu.Get,
		OnClose: close, CloseOnSwipe: true,
		Style:     core.Style{MaxWidth: core.Pt(540), MinHeight: core.Pt(0), CornerRadii: core.Corners{TopLeft: 28, TopRight: 28}},
		StyleFunc: func() core.Style { return core.Style{Background: groupedBackground(cx)} },
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, FlexShrink: 0}, Children: []core.Node{
				core.Text{Content: tr("Conversation actions"), Role: core.RoleHeader,
					Style: core.Style{FontSize: 19, FontWeight: 600, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
				m.navigationAction(cx, "actions-cancel", tr("Cancel"), nswidgets.IconOf("x"), nil, close),
			}},
			core.Scroll{StyleFunc: func() core.Style { return actionSheetScrollStyle(cx) }, Children: []core.Node{
				core.Dynamic(func(cx *core.Scope) core.Node {
					m.revision.Get()
					return m.actionGroups(cx, close)
				}),
			}},
		},
	}
}

func actionSheetScrollStyle(cx *core.Scope) core.Style {
	env := cx.Env()
	safe := env.SafeArea()
	room := env.Window().Height - safe.Top - safe.Bottom - 96
	return core.Style{MinHeight: core.Pt(0), FlexShrink: 1, MaxHeight: core.Pt(max(72, room)), Width: core.Pct(100)}
}

func (m *App) actionGroups(cx *core.Scope, close func()) core.Node {
	entry := func(id, label, icon string, open func()) gRow {
		return gRow{ID: "action-" + id, Label: label, Icon: icon, Press: func() { close(); open() }}
	}
	var tools []gRow
	if m.Connected && m.ComposerProject() != "" {
		tools = append(tools, entry("files", tr("Project files"), "folder", func() { m.pages.Open(""); m.openProjectFile(m.ComposerProject(), "") }))
	}
	if m.Connected && m.ComposerProject() != "" && (m.State.WebPorts || m.State.Runtime) {
		tools = append(tools, entry("web", tr("Web services"), "globe", m.openWebServices))
	}
	if m.Connected && m.State.DesktopControl && m.DesktopRequest == "" {
		tools = append(tools, entry("desktop", tr("Open remote desktop"), "device-desktop", func() {
			m.closeFileBrowser()
			m.pages.Open("")
			m.OpenRemoteDesktop()
		}))
	}
	if m.Open != 0 && m.State.Collaboration != nil && m.State.Collaboration.Version >= 1 {
		tools = append(tools, entry("delegations", tr("Agent tasks"), "stack-2", func() { m.pages.Open("delegations") }))
	}
	tools = append(tools,
		entry("automations", tr("Automations"), "clock", func() { m.pages.Open("automations") }),
		entry("memory", tr("Shared memory"), "bulb", func() { m.pages.Open("memory") }),
	)
	conversations := []gRow{entry("new", tr("New conversation"), "edit", func() {
		m.StartConversation(m.ComposerProject(), m.Agent())
		m.Path = "/thread"
		m.pages.Open("")
	})}
	if m.Open != 0 && !m.Sending {
		conversations = append(conversations, entry("handoff", tr("Hand off to a new conversation"), "arrow-right", func() {
			m.Handoff()
			m.Path = "/thread"
			m.pages.Open("")
		}))
	}
	return core.View{Style: core.Style{Gap: 12, MinWidth: core.Pt(0)}, Children: []core.Node{
		m.group(cx, "", nil, tools...), m.group(cx, "", nil, conversations...),
	}}
}
