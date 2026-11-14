package nsmobile

import (
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/nswidgets"
)

// pageStack holds the child pages pushed over the drawer's pages, as the
// old phone app's router did, and the conversation actions sheet shown over
// everything. The bottom route, "", is the drawer shell itself.
type pageStack struct {
	cx   *core.Scope
	nav  *base.Nav[string]
	menu *core.Signal[bool]
	// actions shows the conversation actions sheet.
	actions func()
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
		if p.actions != nil {
			p.actions()
		} else {
			p.menu.Set(true)
		}
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
	case "quota":
		return tr("Usage")
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
			return base.NavPage{TestID: "page-root", HeaderHidden: true, NoPopOnBack: true, NoPopOnSwipe: true, Style: fill, Children: []core.Node{m.shell(cx)}}
		}
		return base.NavPage{TestID: "page-" + route, HeaderHidden: true, Style: fill,
			StyleFunc: func() core.Style {
				bg := colors.Canvas()
				if groupedRoute(route) {
					bg = groupedBackground(cx)
				}
				return core.Style{Background: bg, Color: colors.Ink()}
			},
			OnDismiss: func() { m.leave(route); m.change() },
			Children: []core.Node{
				m.pageHeader(cx, route),
				core.View{Style: fill, Children: []core.Node{m.pageContent(cx, route)}},
			}}
	})
}

// pageHeader is a pushed page's bar: a round back button and the title.
func (m *App) pageHeader(cx *core.Scope, route string) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Height: core.Pt(56), Padding: core.Symmetric(0, 12), Gap: 8, FlexShrink: 0}, Children: []core.Node{
		m.circle(cx, "page-back", tr("Back"), "arrow-left", func() { m.leave(route); m.pages.Pop() }),
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), AlignItems: core.AlignCenter}, Children: []core.Node{
			core.Text{TestID: "page-title", Content: pageTitle(route), MaxLines: 1, Style: core.Style{FontSize: 16, FontWeight: 600}},
		}},
		core.View{Style: core.Style{Width: core.Pt(44), FlexShrink: 0}},
	}}
}

// pageContent is a pushed page's body.
func (m *App) pageContent(cx *core.Scope, route string) core.Node {
	if strings.HasPrefix(route, "request:") {
		return m.requestSheet(cx, strings.TrimPrefix(route, "request:"))
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
	case "quota":
		return m.quotas(cx)
	case "pair":
		return m.pairView(cx)
	}
	return core.View{}
}
