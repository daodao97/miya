package nschat

import (
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"miya/internal/nswidgets"
)

// composer keeps the shared card slots independent of desktop controllers.
// chat.Composer has no custom editor slot; this card hosts inline rich atoms.
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
	height := core.NewSignal(cx, core.Points(48))
	children := []core.Node{}
	if v.Attachments != nil {
		children = append(children, core.Show{When: func() bool { return v.HasAttachments == nil || v.HasAttachments() },
			Then: func(*core.Scope) core.Node { return v.Attachments }})
	}
	children = append(children, ReferenceEditor{ReferenceTitle: v.ReferenceTitle, Commands: v.Commands, Files: v.FileReferences, CaptureKeys: v.CaptureKeys, OnKey: v.OnComposerKey, OnShortcut: v.OnComposerShortcut, OnEdit: v.OnDraftEdit, OnMove: v.OnComposerMove, Owner: v.ComposerOwner, OnInvalidReference: v.OnInvalidReference, Input: core.Input{TestID: "composer", Label: tr("Message"), Value: v.Draft, OnChange: v.OnDraftChange,
		OnContentSizeChange: func(s core.Size) { height.Set(min(core.Points(200), max(core.Points(48), s.Height))) },
		StyleFunc:           func() core.Style { return core.Style{Height: core.Pt(height.Get())} },
		AutoFocus:           true, Multiline: true, SubmitBehavior: submit, OnSubmit: func(string) {
			send()
		}, OnPaste: v.OnPaste, Disabled: v.EditorDisabled, ReadonlyFunc: v.EditorReadonly, OnKeyPress: func(key string) {
			if key == "mod+enter" && v.OnSteer != nil && has() && !disabled() {
				v.OnSteer()
			}
		},
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
	row = append(row, v.sendButton(cx, has, disabled, send))
	children = append(children, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0), Wrap: core.Wrap}, Children: row})
	dragging := core.NewSignal(cx, false)
	colors := nswidgets.ColorsOf(cx)
	cardStyle := nswidgets.CardStyle(cx)
	return core.View{TestID: "composer-card", StyleFunc: func() core.Style {
		s := cardStyle()
		s.CornerRadius, s.Padding, s.Gap, s.BorderCurve = 16, core.All(12), 8, core.BorderCurveContinuous
		if dragging.Get() {
			s.BorderColor = colors.Link()
			s.Background = colors.Subtle()
		}
		return s
	}, OnFileDragEnter: func() {
		if (v.EditorDisabled == nil || !v.EditorDisabled()) && (v.EditorReadonly == nil || !v.EditorReadonly()) {
			dragging.Set(true)
		}
	}, OnFileDragLeave: func() { dragging.Set(false) }, OnFileDrop: func(uris []string) {
		dragging.Set(false)
		if v.OnDropFiles != nil && (v.EditorDisabled == nil || !v.EditorDisabled()) && (v.EditorReadonly == nil || !v.EditorReadonly()) {
			v.OnDropFiles(uris)
		}
	}, Children: children}
}
