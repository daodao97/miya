package nschat

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"

	"miya/internal/nswidgets"
)

func activityRow(cx *core.Scope, activity func() chat.Activity) core.Node {
	colors := nswidgets.ColorsOf(cx)
	expanded := core.NewSignal(cx, false)
	canExpand := core.NewMemo(cx, func() bool { return activity().Content != nil })
	show := func() bool { return canExpand.Get() && expanded.Get() }
	trailing := core.Show{When: canExpand.Get, Then: func(cx *core.Scope) core.Node {
		icon := core.NewMemo(cx, func() string {
			if show() {
				return "chevron-down"
			}
			return "chevron-right"
		})
		return core.Dynamic(func(cx *core.Scope) core.Node { return nswidgets.Icon(icon.Get(), 11, colors.Muted) })
	}}
	header := []core.Node{
		ui.IconOf(func() *core.SVG {
			if icon := activity().Icon; icon != nil {
				return icon()
			}
			return nswidgets.SVG("terminal-2")
		}, 12, colors.Muted),
		core.Text{ContentFunc: func() string { return activity().Action }, MaxLines: 1, EllipsizeMode: core.EllipsizeTail, Style: core.Style{
			FontSize: 12.5, FontWeight: ui.Semibold, FlexShrink: 1, MinWidth: core.Pt(0),
		}, StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }},
		core.Text{ContentFunc: func() string { return activity().Detail }, MaxLines: 1, EllipsizeMode: core.EllipsizeTail,
			Style:     core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 12},
			StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }},
		core.Show{When: func() bool { return activity().Status == chat.Running }, Then: func(*core.Scope) core.Node {
			return ui.Spinner{Size: 10}
		}},
		trailing,
	}
	shape := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Height: core.Pt(28), Padding: core.Symmetric(0, 8), Gap: 8, MinWidth: core.Pt(0)}
	return core.View{StyleFunc: func() core.Style {
		return core.Style{Background: colors.Subtle(), BorderWidth: 1, BorderColor: colors.Line(), CornerRadius: 9, Overflow: core.Hidden, MinWidth: core.Pt(0)}
	}, Children: []core.Node{
		core.Dynamic(func(cx *core.Scope) core.Node {
			if !canExpand.Get() {
				return core.View{Style: shape, Children: header}
			}
			return base.Button{Style: shape, Expanded: show, Children: header, OnPress: func(core.PressEvent) {
				expanded.Update(func(on bool) bool { return !on })
			}}
		}),
		core.Show{When: show, Then: func(cx *core.Scope) core.Node {
			scroll := &core.Ref{}
			follow := true
			return core.Scroll{Ref: scroll, Style: core.Style{MaxHeight: core.Pt(400)}, OnScroll: func(e core.ScrollEvent) {
				follow = e.Offset.Y+e.Size.Height >= e.ContentSize.Height-2
			}, Children: []core.Node{core.View{Style: core.Style{Padding: core.All(10), MinWidth: core.Pt(0)}, OnLayout: func(core.Bounds) {
				if follow && core.Untracked(cx, activity).Status == chat.Running {
					scroll.ScrollToEnd(false)
				}
			}, Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
				if content := activity().Content; content != nil {
					return content(cx)
				}
				return core.Group(nil)
			})}}}}
		}},
	}}
}
