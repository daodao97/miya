package nsmobile

import (
	"miya/internal/nswidgets"

	"github.com/nsgo-dev/nsgo/core"
)

// Every interactive row is at least 44 points tall, and lets larger system
// text grow vertically instead of clipping a fixed-size target.
func (m *App) button(id, label string, disabled func() bool, press func()) core.Node {
	colors := nswidgets.ColorsOf(m.cx)
	return core.View{TestID: id, Role: core.RoleButton, Label: label, Disabled: disabled,
		StyleFunc: func() core.Style {
			return core.Style{MinHeight: core.Pt(44), Padding: core.Symmetric(10, 12), JustifyContent: core.JustifyCenter, CornerRadius: 10, Background: colors.Bubble(), FlexShrink: 0}
		},
		OnPress: func(core.PressEvent) {
			if disabled == nil || !disabled() {
				m.act(press)()
			}
		}, Children: []core.Node{core.Text{Content: label, Style: core.Style{FontSize: 16}}}}
}
func column(nodes ...core.Node) core.Node {
	return core.View{Style: core.Style{Gap: 10, MinWidth: core.Pt(0)}, Children: nodes}
}
func row(nodes ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Wrap: core.Wrap}, Children: nodes}
}
func scroll(nodes ...core.Node) core.Node {
	return core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100)}, Children: []core.Node{core.View{Style: core.Style{Padding: core.All(12), Gap: 12}, Children: nodes}}}
}
func text(s string) core.Node { return core.Text{Content: s, Style: core.Style{FontSize: 16}} }
func (m *App) errorText(cx *core.Scope) core.Node {
	return core.Text{TestID: "mobile-error", ContentFunc: live(m, func() string { return m.SendErr }), StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Danger(), FontSize: 14} }}
}
func (m *App) toolbar(title func() string, back bool) core.Node {
	left := m.button("settings", tr("Settings"), nil, func() { m.sheet.Set("settings") })
	if back {
		left = m.button("back", tr("Back"), nil, func() { m.Home() })
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(4, 8), FlexShrink: 0}, Children: []core.Node{left,
		core.Text{TestID: "mobile-title", ContentFunc: title, MaxLines: 2, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 18, FontWeight: 600}},
		m.button("conversation-actions", tr("More"), nil, func() { m.sheet.Set("actions") }),
	}}
}
