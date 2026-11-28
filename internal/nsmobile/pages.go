package nsmobile

import (
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

// pageStack holds the child pages pushed over the drawer's pages, as the
// old phone app's router did, and the conversation actions sheet shown over
// everything. The bottom route, "", is the drawer shell itself.
type pageStack struct {
	cx   *core.Scope
	nav  *base.Nav[string]
	menu *core.Signal[bool]
}

func newPages(cx *core.Scope) *pageStack {
	return &pageStack{cx: cx, nav: base.NewNav(cx, ""), menu: core.NewSignal(cx, false)}
}

// Top is what shows on top: "actions" while the actions sheet does, else
// the top page's route, "" with none.
func (p *pageStack) Top() string {
	if p.menu.Get() {
		return "actions"
	}
	routes := p.nav.Routes()
	return routes[len(routes)-1]
}

// Depth is how many pages are pushed.
func (p *pageStack) Depth() int { return p.nav.Depth() }

// Open shows route: "" closes every page and the sheet, "actions" opens the
// actions sheet, a route already open goes back to it, and another is
// pushed.
func (p *pageStack) Open(route string) {
	switch route {
	case "":
		p.menu.Set(false)
		p.nav.PopToRoot()
		return
	case "actions":
		p.menu.Set(true)
		return
	}
	p.menu.Set(false)
	routes := core.Untracked(p.cx, p.nav.Routes)
	for i := len(routes) - 1; i > 0; i-- {
		if routes[i] == route {
			p.nav.Pop(len(routes) - 1 - i)
			return
		}
	}
	p.nav.Push(route)
}

// Pop goes back one page.
func (p *pageStack) Pop() { p.nav.Pop(1) }

func pageTitle(route string) string {
	if strings.HasPrefix(route, "request:") {
		return tr("Pending approval")
	}
	if strings.HasPrefix(route, "desktop-edit:") {
		return tr("Desktop name")
	}
	switch route {
	case "settings":
		return tr("Settings")
	case "language":
		return tr("Interface language")
	case "voice":
		return tr("Voice input")
	case "push":
		return tr("Notifications")
	case "options":
		return tr("Conversation settings")
	case "agent":
		return tr("Agent")
	case "model":
		return tr("Model")
	case "effort":
		return tr("Reasoning effort")
	case "policy":
		return tr("Permission mode")
	case "automations", "automation":
		return tr("Automations")
	case "automation-new":
		return tr("New automation")
	case "memory":
		return tr("Shared memory")
	case "desktops":
		return tr("Desktops")
	case "pair":
		return tr("Add desktop")
	case "web-services":
		return tr("Web services")
	case "delegations":
		return tr("Agent tasks")
	case "projects":
		return tr("Select project")
	}
	return ""
}

// leave releases what a page owns as the user goes back from it.
func (m *App) leave(route string) {
	if route == "web-services" {
		m.closeWebServices()
	}
}

// stack is the drawer shell under the pushed pages: iOS swipes back from a
// page's leading edge and Android's back pops it.
func (m *App) stack(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	fill := core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100)}
	return m.pages.nav.Stack(fill, func(cx *core.Scope, route string, _ func() int) base.NavPage {
		if route == "" {
			return base.NavPage{TestID: "page-root", HeaderHidden: true, HeaderBackTitle: tr("Back"), NoPopOnBack: true, NoPopOnSwipe: true, Style: fill,
				StyleFunc: func() core.Style {
					bottom := pageBottomInset(cx, m.hasFloatingComposer())
					background := colors.Canvas()
					if m.Loaded && m.Link == "" {
						background = groupedBackground(cx)
					}
					return core.Style{Padding: core.Edges{Bottom: bottom}, Background: background}
				}, Children: []core.Node{core.Func(m.shell)}}
		}
		title := pageTitle(route)
		trailing := m.pageActions(cx, route)
		// The platform stack still handles swiping back. Draw the control
		// in the screen so UIKit cannot drop its custom bar view on a push.
		return base.NavPage{TestID: "page-" + route, Title: title, HeaderHidden: true, Style: fill,
			StyleFunc: func() core.Style {
				return core.Style{Background: groupedBackground(cx), Color: colors.Ink(), Padding: core.Edges{Bottom: pageBottomInset(cx, false)}}
			},
			OnDismiss: func() { m.leave(route); m.change() },
			Children: []core.Node{
				m.navigationBar(cx, title, "page-back", m.pages.Pop, trailing...),
				core.View{Style: fill, Children: []core.Node{m.pageContent(cx, route)}},
			},
		}
	})
}

const (
	navigationButtonSize core.Points = 44
	navigationButtonGap  core.Points = 8
)

func (m *App) navigationBar(cx *core.Scope, title, backID string, back func(), trailing ...core.Node) core.Node {
	backIcon := func() *core.SVG {
		if cx.Env().RTL() {
			return nswidgets.SVG("arrow-right")
		}
		return nswidgets.SVG("arrow-left")
	}
	// Match the width of both sides so the title stays centered with zero,
	// one, or two toolbar actions.
	sideWidth := navigationButtonSize + core.Points(max(0, len(trailing)-1))*(navigationButtonSize+navigationButtonGap)
	side := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, FlexShrink: 0,
		Width: core.Pt(sideWidth), Gap: navigationButtonGap}
	return core.View{TestID: "page-header", Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
		Width: core.Pct(100), MinHeight: core.Pt(56), FlexShrink: 0, Gap: 8, Padding: core.Symmetric(6, 12)},
		Children: []core.Node{
			core.View{Style: side, Children: []core.Node{m.navigationAction(cx, backID, tr("Back"), backIcon, nil, back)}},
			core.Text{TestID: "page-title", Content: title, MaxLines: 2, Role: core.RoleHeader,
				Style: core.Style{FontSize: 17, FontWeight: 600, TextAlign: core.TextAlignCenter, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
			core.View{Style: side.Merge(core.Style{JustifyContent: core.JustifyEnd}), Children: trailing},
		}}
}

func (m *App) navigationAction(cx *core.Scope, id, label string, icon func() *core.SVG, disabled func() bool, press func()) ui.IconButton {
	colors := nswidgets.ColorsOf(cx)
	return ui.IconButton{
		TestID: id, Label: label, Disabled: disabled, OnPress: m.act(press),
		Icon: icon, IconSize: 20, Size: navigationButtonSize, Variant: ui.IconButtonFilled,
		Tint: colors.Ink, Fill: func() core.Color {
			return ui.Mix(groupedBackground(cx), colors.Ink(), 0.075)
		},
	}
}

func (m *App) pageActions(cx *core.Scope, route string) []core.Node {
	action := func(id, label, icon string, disabled func() bool, press func()) core.Node {
		return m.navigationAction(cx, id, label, nswidgets.IconOf(icon), disabled, press)
	}
	switch route {
	case "memory":
		return []core.Node{action("memory-refresh", tr("Refresh memory"), "refresh", live(m, func() bool {
			return !m.Connected || !m.State.Memory || m.Memories.Loading
		}), m.LoadMemory)}
	case "web-services":
		return []core.Node{action("refresh-web", tr("Refresh web services"), "refresh", live(m, func() bool {
			return m.web == nil || m.web.loading || !m.Connected
		}), func() {
			if m.web != nil {
				m.refreshWebServices(m.web)
			}
		})}
	case "automations":
		return []core.Node{
			action("automations-refresh", tr("Refresh automations"), "refresh", live(m, func() bool {
				return !m.Connected || !m.State.Automations || m.Autos.Loading
			}), m.LoadAutomations),
			action("automation-new", tr("New automation"), "plus", live(m, func() bool {
				return !m.Connected || !m.State.AutomationCreate
			}), func() { m.OpenAutomationCreate(); m.pages.Open("automation-new") }),
		}
	case "desktops":
		return []core.Node{action("desktop-add", tr("Add desktop"), "plus", nil, func() { m.pages.Open("pair") })}
	}
	return nil
}

// pageContent is a pushed page's body.
func (m *App) pageContent(cx *core.Scope, route string) core.Node {
	if strings.HasPrefix(route, "request:") {
		return m.requestSheet(cx, strings.TrimPrefix(route, "request:"))
	}
	if strings.HasPrefix(route, "desktop-edit:") {
		return m.desktopEdit(cx, strings.TrimPrefix(route, "desktop-edit:"))
	}
	switch route {
	case "web-services":
		return m.webServicesSheet(cx)
	case "voice":
		return m.voiceSettings(cx)
	case "push":
		return m.pushSettings(cx)
	case "memory":
		return m.memorySheet(cx)
	case "automations", "automation", "automation-new":
		return m.automationsSheet(cx, route)
	case "delegations":
		return m.delegationsSheet(cx)
	case "desktops":
		return m.desktopsView(cx)
	case "settings":
		return m.settings(cx)
	case "language":
		return m.languageSettings(cx)
	case "projects":
		return m.projects(cx)
	case "options", "agent", "model", "effort", "policy":
		return m.options(cx, route)
	case "pair":
		return m.pairView(cx, false)
	}
	return core.View{}
}
