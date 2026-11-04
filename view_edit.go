package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// userGroupView shows a user message, with an edit button while the pointer
// is on it when its agent can send it again.
func (app *chatApp) userGroupView(c *ui.Context, g messageGroup) {
	m := g.Messages[0]
	// The phone holds no agent to rewind with.
	editable := !app.mobile && app.c.canRewind(app.c.current(), m)
	col := ui.Column(c).FillWidth().Gap(4)
	hovered := col.Hovered()
	col.Children(func() {
		if g.ShowTimestamp {
			ui.Text(c, formatMessageTimestamp(g.Timestamp)).AlignSelf(ui.End).TextColor(muted).FontSize(11)
		}
		if app.editing == m.ID && editable {
			app.editMessageView(c.Key("edit"), m)
			return
		}
		app.messageView(c.Key(messageViewKey(m)), m, false)
		if !editable {
			return
		}
		ui.Row(c).Height(24).Justify(ui.End).Children(func() {
			if !hovered {
				return
			}
			b := quiet(c.Key("edit-message")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Edit message")).Tooltip(tr("Edit and resend"))
			b.Children(func() { icon(c, "edit", 14, muted) })
			if b.Clicked() {
				app.startEdit(m)
			}
		})
	})
}

// startEdit edits a message's text, without the attachment lines the
// composer added to it.
func (app *chatApp) startEdit(m Message) {
	m.Content = claudeCommandText(m.Content)
	var keep []string
	for _, l := range strings.Split(attachmentImagePlaceholder.ReplaceAllString(m.Content, ""), "\n") {
		if !strings.HasPrefix(l, "📎 ") {
			keep = append(keep, l)
		}
	}
	body, _ := app.threadReferenceContent(strings.TrimSpace(strings.Join(keep, "\n")))
	app.editing, app.editText = m.ID, body
}

func (app *chatApp) submitEdit(m Message) {
	if strings.TrimSpace(app.editText) == "" {
		return
	}
	app.editing = 0
	app.c.rewind(app.c.current().ID, m.ID, app.editText)
}

func (app *chatApp) editMessageView(c *ui.Context, m Message) {
	ui.Column(c).AlignSelf(ui.End).FillWidth().MaxWidth(560).Background(bubble).Radius(16).Padding(12).Gap(8).Children(func() {
		in := app.threadInput(c.Key("input"), &app.editInput, app.editText, strconv.Itoa(m.ID), tr("Edit message"), "", 13, 536, 1, 12, func(text string) { app.editText = text }).AutoFocus()
		if in.Shortcut(0, ui.KeyEnter) {
			app.submitEdit(m)
		}
		if in.Shortcut(0, ui.KeyEscape) {
			app.editing = 0
		}
		ui.Text(c, tr("Restart from this message. Later messages will be deleted. Miya will not undo file changes already made.")).FontSize(11).TextColor(muted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				app.editing = 0
			}
			if ui.PrimaryButton(c.Key("send"), tr("Send")).Disabled(strings.TrimSpace(app.editText) == "").Clicked() {
				app.submitEdit(m)
			}
		})
	})
}
