package nsui

import (
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nschat"
	"miya/internal/nswidgets"
)

func editBody(m model.Message) string {
	var lines []string
	for _, l := range strings.Split(model.ImagePlaceholder.ReplaceAllString(msgfmt.Message(m).Content, ""), "\n") {
		if !strings.HasPrefix(l, "📎 ") {
			lines = append(lines, l)
		}
	}
	body, _ := model.SplitReferenceContext(strings.TrimSpace(strings.Join(lines, "\n")))
	return body
}
func (d *desktopComposer) userContent(cx *core.Scope, get func() model.Message, body core.Node) core.Node {
	id := strconv.Itoa(core.Untracked(cx, func() int { return get().ID }))
	editing, text := core.NewSignal(cx, false), core.NewSignal(cx, "")
	// Reset edits on navigation, including threads with overlapping message IDs.
	owner := core.Untracked(cx, func() int { return d.host.Current().ID })
	editable := Live(func() bool { return d.host.Current().ID == owner && d.host.CanRewind(d.host.Current(), get()) })
	save := func() {
		if !editable() || strings.TrimSpace(text.Get()) == "" {
			return
		}
		editing.Set(false)
		d.host.Rewind(owner, get().ID, text.Get())
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if editing.Get() && editable() {
			cx.OnKeyWith("escape", core.KeyOptions{When: core.KeyAlways}, func() { editing.Set(false) })
			return core.View{TestID: "editing-message-" + id, StyleFunc: nswidgets.CardStyle(cx), Children: []core.Node{
				nschat.ReferenceInput{ReferenceTitle: d.referenceTitle, Input: core.Input{TestID: "edit-message-input-" + id, Label: tr("Edit message"), Value: text.Get, OnChange: text.Set, AutoFocus: true, Multiline: true, SubmitBehavior: core.SubmitBehaviorSubmit, OnSubmit: func(string) { save() }, Style: core.Style{MinHeight: core.Pt(60), FontSize: 13}}},
				nswidgets.MutedText(cx, func() string {
					return tr("Restart from this message. Later messages will be deleted. Miya will not undo file changes already made.")
				}),
				settingsActions(ui.Button{Text: tr("Cancel"), TestID: "edit-cancel-" + id, OnPress: func() { editing.Set(false) }}, ui.Button{Text: tr("Send"), TestID: "edit-send-" + id, OnPress: save, Disabled: func() bool { return strings.TrimSpace(text.Get()) == "" }}),
			}}
		}
		return settingsColumn(body, core.Show{When: editable, Then: func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{AlignSelf: core.AlignEnd}, Children: []core.Node{ui.IconButton{TestID: "edit-message-" + id, Label: tr("Edit message"), Icon: nswidgets.IconOf("edit"), OnPress: func() { text.Set(editBody(get())); editing.Set(true) }}}}
		}})
	})
}
