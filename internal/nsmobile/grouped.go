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
	ID, Label, Icon string
	LabelFunc       func() string
	Leading         core.Node
	Detail          string
	DetailFunc      func() string
	Value           func() string
	ValueID         string
	Chevron         bool
	Checked         func() bool
	Switch          *ui.Switch
	Action          bool
	Destructive     bool
	Disabled        func() bool
	Press           func()
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

// groupedPage scrolls groups 24 points apart.
func (m *App) groupedPage(cx *core.Scope, groups ...core.Node) core.Node {
	return core.Scroll{TestID: "grouped-scroll", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100)},
		StyleFunc: func() core.Style { return core.Style{Background: groupedBackground(cx)} },
		Children:  []core.Node{core.View{Style: core.Style{Width: core.Pct(100), Padding: core.Edges{Top: 16, Left: 16, Right: 16, Bottom: 28}, Gap: 24}, Children: groups}}}
}

// group is a card of rows under an optional title, over an optional footer.
func (m *App) group(cx *core.Scope, title string, footer core.Node, rows ...gRow) core.Node {
	var cells []core.Node
	for i, r := range rows {
		if i > 0 {
			cells = append(cells, groupedDivider(cx))
		}
		cells = append(cells, m.groupedRow(cx, r))
	}
	return groupedSection(cx, title, footer, cells...)
}

func groupedDivider(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{Style: core.Style{Height: core.Pt(1), Margin: core.Edges{Left: 16}},
		StyleFunc: func() core.Style { return core.Style{Background: colors.Line()} }}
}

func groupedSection(cx *core.Scope, title string, footer core.Node, cells ...core.Node) core.Node {
	colors := nswidgets.ColorsOf(cx)
	var nodes []core.Node
	if title != "" {
		nodes = append(nodes, core.Text{Content: title, Role: core.RoleHeader, StyleFunc: func() core.Style {
			return core.Style{FontSize: 13, FontWeight: 500, Color: colors.Muted(), Padding: core.Symmetric(0, 16)}
		}})
	}
	if len(cells) > 0 {
		nodes = append(nodes, core.View{Style: core.Style{Width: core.Pct(100), MinWidth: core.Pt(0), CornerRadius: 14, Overflow: core.Hidden},
			StyleFunc: func() core.Style { return core.Style{Background: groupedSurface(cx)} }, Children: cells})
	}
	if footer != nil {
		nodes = append(nodes, footer)
	}
	return core.View{Style: core.Style{Width: core.Pct(100), Gap: 8}, Children: nodes}
}

func groupedState(cx *core.Scope, icon string, message func() string, busy, failed func() bool) core.Node {
	colors := nswidgets.ColorsOf(cx)
	active := func() bool { return busy != nil && busy() }
	return core.Show{When: func() bool { return message() != "" }, Then: func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.All(16), MinHeight: core.Pt(56), CornerRadius: 14},
			StyleFunc: func() core.Style { return core.Style{Background: groupedSurface(cx)} },
			Children: []core.Node{
				core.Show{When: active, Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 17, Tint: colors.Muted} },
					Else: func(*core.Scope) core.Node { return nswidgets.Icon(icon, 19, colors.Muted) }},
				core.Text{ContentFunc: message, StyleFunc: func() core.Style {
					color := colors.Muted()
					if failed != nil && failed() {
						color = colors.Danger()
					}
					return core.Style{FontSize: 15, LineHeight: 21, Color: color, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}
				}},
			},
		}
	}}
}

// groupedFooter is a group's muted note.
func groupedFooter(cx *core.Scope, id string, text func() string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Show{When: func() bool { return text() != "" }, Then: func(*core.Scope) core.Node {
		return core.Text{TestID: id, ContentFunc: text, StyleFunc: func() core.Style {
			return core.Style{FontSize: 13, LineHeight: 17, Color: colors.Muted(), Padding: core.Symmetric(0, 16)}
		}}
	}}
}

func (m *App) groupedRow(cx *core.Scope, r gRow) core.Node {
	if r.LabelFunc != nil {
		r.Label = ""
	}
	if r.DetailFunc != nil {
		r.Detail = ""
	}
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
	texts := []core.Node{core.Text{Content: r.Label, ContentFunc: r.LabelFunc, StyleFunc: func() core.Style { return core.Style{FontSize: 17, Color: labelColor()} }}}
	if r.Detail != "" || r.DetailFunc != nil {
		texts = append(texts, core.Text{Content: r.Detail, ContentFunc: r.DetailFunc, StyleFunc: func() core.Style {
			return core.Style{FontSize: 13, LineHeight: 17, Color: colors.Muted()}
		}})
	}
	var children []core.Node
	if r.Leading != nil {
		children = append(children, r.Leading)
	} else if r.Icon != "" {
		children = append(children, nswidgets.Icon(r.Icon, 20, colors.Link))
	}
	children = append(children, core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 3}, Children: texts})
	if r.Value != nil {
		children = append(children, core.Text{TestID: r.ValueID, ContentFunc: r.Value, MaxLines: 1, EllipsizeMode: core.EllipsizeTail, StyleFunc: func() core.Style {
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
		return core.View{TestID: r.ID, Style: style, Children: children}
	}
	role := core.RoleButton
	if r.Checked != nil {
		role = core.RoleRadio
	}
	return core.View{TestID: r.ID, Role: role, Label: r.Label, LabelFunc: r.LabelFunc, Checked: r.Checked, AccessibilityDescription: r.Detail,
		AccessibilityDescriptionFunc: r.DetailFunc, Disabled: r.Disabled, Style: style,
		StyleFunc: func() core.Style {
			return core.Style{Active: &core.Style{Background: colors.Hover()}, FocusVisible: ui.FocusRing(cx, -2)}
		},
		OnPress: func(core.PressEvent) { m.act(r.Press)() }, Children: children}
}
