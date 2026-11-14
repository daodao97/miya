package nsmobile

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

// The settings pages are iOS grouped lists, as the old phone app's: cards
// of 52-point rows on a page of their own shade, a header over each card
// and a footer under it.

// gRow is one row of a grouped list. A row with Press is a button; Value is
// muted text at its end, Chevron leads to a page, Checked marks the chosen
// one of a choice, and Switch ends it with a switch instead.
type gRow struct {
	ID, Label   string
	Detail      string
	Value       func() string
	ValueID     string
	Chevron     bool
	Checked     func() bool
	Switch      *ui.Switch
	Action      bool
	Destructive bool
	Disabled    func() bool
	Press       func()
}

// groupedBackground is the page's shade and groupedSurface its cards': the
// cards stand out from it in light and dark mode alike, as iOS's.
func groupedBackground(cx *core.Scope) core.Color {
	colors := nswidgets.ColorsOf(cx)
	if cx.Env().Dark() {
		return colors.Canvas()
	}
	return colors.Bubble()
}

func groupedSurface(cx *core.Scope) core.Color {
	colors := nswidgets.ColorsOf(cx)
	if cx.Env().Dark() {
		return colors.Bubble()
	}
	return colors.Canvas()
}

// groupedRoute reports whether a pushed page is a grouped list, whose header
// takes the list's shade.
func groupedRoute(route string) bool {
	switch route {
	case "settings", "language", "voice", "push", "options", "agent", "model", "effort", "policy":
		return true
	}
	return false
}

// groupedPage scrolls groups 24 points apart.
func (m *App) groupedPage(cx *core.Scope, groups ...core.Node) core.Node {
	return core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100)},
		StyleFunc: func() core.Style { return core.Style{Background: groupedBackground(cx)} },
		Children:  []core.Node{core.View{Style: core.Style{Width: core.Pct(100), Padding: core.Edges{Top: 16, Left: 16, Right: 16, Bottom: 28}, Gap: 24}, Children: groups}}}
}

// group is a card of rows under an optional title, over an optional footer.
func (m *App) group(cx *core.Scope, title string, footer core.Node, rows ...gRow) core.Node {
	colors := nswidgets.ColorsOf(cx)
	var cells []core.Node
	for i, r := range rows {
		if i > 0 {
			cells = append(cells, core.View{Style: core.Style{Height: core.Pt(1), Margin: core.Edges{Left: 16}}, StyleFunc: func() core.Style { return core.Style{Background: colors.Line()} }})
		}
		cells = append(cells, m.groupedRow(cx, r))
	}
	var nodes []core.Node
	if title != "" {
		nodes = append(nodes, core.Text{Content: title, StyleFunc: func() core.Style {
			return core.Style{FontSize: 13, Color: colors.Muted(), Padding: core.Symmetric(0, 16)}
		}})
	}
	nodes = append(nodes, core.View{Style: core.Style{Width: core.Pct(100), CornerRadius: 12, Overflow: core.Hidden},
		StyleFunc: func() core.Style { return core.Style{Background: groupedSurface(cx)} }, Children: cells})
	if footer != nil {
		nodes = append(nodes, footer)
	}
	return core.View{Style: core.Style{Width: core.Pct(100), Gap: 8}, Children: nodes}
}

// groupedFooter is a group's muted note.
func groupedFooter(cx *core.Scope, id string, text func() string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Text{TestID: id, ContentFunc: text, StyleFunc: func() core.Style {
		return core.Style{FontSize: 13, LineHeight: 17, Color: colors.Muted(), Padding: core.Symmetric(0, 16)}
	}}
}

func (m *App) groupedRow(cx *core.Scope, r gRow) core.Node {
	colors := nswidgets.ColorsOf(cx)
	off := func() bool { return r.Disabled != nil && r.Disabled() }
	labelColor := func() core.Color {
		switch {
		case off():
			return colors.Muted()
		case r.Destructive:
			return colors.Danger()
		case r.Action:
			return colors.Link()
		}
		return colors.Ink()
	}
	texts := []core.Node{core.Text{Content: r.Label, StyleFunc: func() core.Style { return core.Style{FontSize: 17, Color: labelColor()} }}}
	if r.Detail != "" {
		texts = append(texts, core.Text{Content: r.Detail, StyleFunc: func() core.Style {
			return core.Style{FontSize: 13, LineHeight: 17, Color: colors.Muted()}
		}})
	}
	children := []core.Node{core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 3}, Children: texts}}
	if r.Value != nil {
		children = append(children, core.Text{TestID: r.ValueID, ContentFunc: r.Value, MaxLines: 1, StyleFunc: func() core.Style {
			return core.Style{FontSize: 16, Color: colors.Muted(), MaxWidth: core.Pct(45), FlexShrink: 1, TextAlign: core.TextAlignRight}
		}})
	}
	if r.Checked != nil {
		children = append(children, core.Show{When: r.Checked, Then: func(*core.Scope) core.Node { return nswidgets.Icon("check", 18, colors.Link) }})
	}
	if r.Chevron {
		children = append(children, nswidgets.Icon("chevron-right", 14, colors.Muted))
	}
	style := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, MinHeight: core.Pt(52), Padding: core.Symmetric(12, 16), Width: core.Pct(100)}
	if r.Switch != nil {
		children = append(children, *r.Switch)
		style.Padding = core.Symmetric(4, 16)
		return core.View{Style: style, Children: children}
	}
	if r.Press == nil {
		return core.View{Style: style, Children: children}
	}
	press := r.Press
	return core.View{TestID: r.ID, Role: core.RoleButton, Label: r.Label, Disabled: r.Disabled, Style: style,
		OnPress: func(core.PressEvent) {
			if !off() {
				m.act(press)()
			}
		}, Children: children}
}
