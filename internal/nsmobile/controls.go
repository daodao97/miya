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

// circle is a round 44-point button on the surface color, as headers have.
func (m *App) circle(cx *core.Scope, id, label, icon string, press func()) core.Node {
	return m.round(cx, id, label, icon, 44, press)
}

func (m *App) round(cx *core.Scope, id, label, icon string, size core.Points, press func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{TestID: id, Role: core.RoleButton, Label: label, OnPress: func(core.PressEvent) { m.act(press)() },
		StyleFunc: func() core.Style {
			return core.Style{Width: core.Pt(size), Height: core.Pt(size), CornerRadius: size / 2, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Background: colors.Bubble()}
		},
		Children: []core.Node{nswidgets.Icon(icon, 20, colors.Ink)}}
}

// iconAction is a navigation button: a full touch target without a disc.
func (m *App) iconAction(cx *core.Scope, id, label, icon string, press func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{TestID: id, Role: core.RoleButton, Label: label, OnPress: func(core.PressEvent) { m.act(press)() },
		Style:    core.Style{Width: core.Pt(44), Height: core.Pt(44), CornerRadius: 12, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
		Children: []core.Node{nswidgets.Icon(icon, 23, colors.Ink)}}
}

// dot is a round status mark.
func dot(size core.Points, color func() core.Color) core.Node {
	return core.View{StyleFunc: func() core.Style {
		return core.Style{Width: core.Pt(size), Height: core.Pt(size), CornerRadius: size / 2, FlexShrink: 0, Background: color()}
	}}
}
