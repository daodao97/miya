package nschat

import (
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/nswidgets"
)

// composer keeps the shared card slots independent of desktop controllers.
// Unlike chat.Composer it permits attachment-only sends and a disabled editor.
func (v View) composer(cx *core.Scope) core.Node {
	has := func() bool {
		return strings.TrimSpace(v.Draft()) != "" || v.HasAttachments != nil && v.HasAttachments()
	}
	disabled := func() bool { return v.SendDisabled != nil && v.SendDisabled() }
	send := func() {
		if has() && !disabled() && v.OnSend != nil {
			v.State.JumpToLatest()
			v.OnSend()
		}
	}
	submit := core.SubmitBehaviorNewline
	if cx.Env().Desktop() {
		submit = core.SubmitBehaviorSubmit
	}
	focused := core.NewSignal(cx, false)
	height := core.NewSignal(cx, core.Points(48))
	cx.OnKey("mod+enter", func() {
		if focused.Get() && v.OnSteer != nil && has() && !disabled() {
			v.OnSteer()
		}
	})
	children := []core.Node{v.referenceChips(cx, v.Draft, v.OnDraftChange)}
	if v.Attachments != nil {
		children = append(children, v.Attachments)
	}
	children = append(children, ReferenceInput{ReferenceTitle: v.ReferenceTitle, Input: core.Input{TestID: "composer", Label: tr("Message"), Value: v.Draft, OnChange: v.OnDraftChange,
		OnFocus: func() { focused.Set(true) }, OnBlur: func() { focused.Set(false) },
		OnContentSizeChange: func(s core.Size) { height.Set(min(core.Points(200), max(core.Points(48), s.Height))) },
		StyleFunc:           func() core.Style { return core.Style{Height: core.Pt(height.Get())} },
		AutoFocus:           true, Multiline: true, SubmitBehavior: submit, OnSubmit: func(string) {
			if v.OnComposerSubmit == nil || !v.OnComposerSubmit() {
				send()
			}
		}, OnPaste: v.OnPaste, Disabled: v.EditorDisabled,
		PlaceholderFunc: func() string {
			if v.Thread().Busy {
				return tr("Enter to queue, ⌘Enter to steer now")
			}
			return tr("Message… / Commands · @ Files")
		},
		Style: core.Style{MinHeight: core.Pt(48), MaxHeight: core.Pt(200), FontSize: 13, Padding: core.All(4)},
	}})
	row := []core.Node{}
	if v.Accessory != nil {
		row = append(row, v.Accessory)
	}
	row = append(row, core.View{Style: core.Style{FlexGrow: 1}})
	if v.Trailing != nil {
		row = append(row, v.Trailing)
	}
	row = append(row, core.Dynamic(func(cx *core.Scope) core.Node {
		if v.Thread().Busy && !has() {
			return ui.Button{Text: tr("Stop"), TestID: "composer-stop", OnPress: v.OnStop, Disabled: disabled, Size: ui.SizeSmall}
		}
		label := tr("Send")
		if v.Thread().Busy {
			label = tr("Add to queue")
		}
		return ui.Button{Text: label, TestID: "composer-send", OnPress: send, Disabled: func() bool { return !has() || disabled() }, Size: ui.SizeSmall}
	}))
	children = append(children, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0), Wrap: core.Wrap}, Children: row})
	dragging := core.NewSignal(cx, false)
	colors := nswidgets.ColorsOf(cx)
	cardStyle := nswidgets.CardStyle(cx)
	return core.View{TestID: "composer-card", StyleFunc: func() core.Style {
		s := cardStyle()
		if dragging.Get() {
			s.BorderColor = colors.Link()
			s.Background = colors.Subtle()
		}
		return s
	}, OnFileDragEnter: func() { dragging.Set(true) }, OnFileDragLeave: func() { dragging.Set(false) }, OnFileDrop: func(uris []string) {
		dragging.Set(false)
		if v.OnDropFiles != nil {
			v.OnDropFiles(uris)
		}
	}, Children: children}
}
