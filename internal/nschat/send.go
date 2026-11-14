package nschat

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

// sendButton is the composer's round send control, as Miya's window always
// had it: an arrow that fills with ink once there is something to send, and
// a square stopping the running turn while the draft is empty.
func (v View) sendButton(cx *core.Scope, has, disabled func() bool, send func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	stopping := core.NewMemo(cx, func() bool { return v.Thread().Busy && !has() })
	label := func() string {
		switch {
		case stopping.Get():
			return tr("Stop")
		case v.Thread().Busy:
			return tr("Add to queue")
		}
		return tr("Send")
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		id, off, press := "composer-send", func() bool { return !has() || disabled() }, send
		var glyph core.Node = nswidgets.Icon("arrow-up", 16, func() core.Color {
			if off() {
				return colors.Muted()
			}
			return colors.Canvas()
		})
		if stopping.Get() {
			id, off = "composer-stop", disabled
			press = func() {
				if v.OnStop != nil {
					v.OnStop()
				}
			}
			glyph = core.View{StyleFunc: func() core.Style {
				return core.Style{Width: core.Pt(10), Height: core.Pt(10), CornerRadius: 2.5, Background: colors.Canvas()}
			}}
		}
		return ui.Tooltip{TextFunc: label, Style: core.Style{FlexShrink: 0}, Trigger: core.View{TestID: id, Role: core.RoleButton, LabelFunc: label, Disabled: off,
			OnPress: func(core.PressEvent) {
				if !off() {
					press()
				}
			},
			Style: core.Style{Width: core.Pt(30), Height: core.Pt(30), CornerRadius: 15, FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			StyleFunc: func() core.Style {
				if off() {
					return core.Style{Background: colors.Chosen()}
				}
				return core.Style{Background: colors.Ink(), Hover: &core.Style{Opacity: core.Alpha(0.82)}}
			}, Children: []core.Node{glyph}}}
	})
}
