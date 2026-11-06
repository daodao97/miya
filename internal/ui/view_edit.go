package desktopui

import (
	appmodel "miya/internal/appmodel"
	model "miya/internal/model"
	widgets "miya/internal/widgets"
	strconv "strconv"
	strings "strings"

	ui "github.com/egoist/mygo/ui"
)

// userGroupView shows a user message, with an edit button while the pointer
// is on it when its agent can send it again.
func (app *App) userGroupView(c *ui.Context, g appmodel.MessageGroup) {
	m := g.Messages[0]
	// The phone holds no agent to rewind with.
	editable := !app.Mobile && app.C.CanRewind(app.C.Current(), m)
	col := ui.Column(c).FillWidth().Gap(4)
	hovered := col.Hovered()
	col.Children(func() {
		if g.ShowTimestamp {
			ui.Text(c, appmodel.FormatMessageTimestamp(g.Timestamp)).AlignSelf(ui.End).TextColor(widgets.Muted).FontSize(11)
		}
		if app.Editing == m.ID && editable {
			app.editMessageView(c.Key("edit"), m)
			return
		}
		app.MessageView(c.Key(appmodel.MessageViewKey(m)), m, false)
		if !editable {
			return
		}
		ui.Row(c).Height(24).Justify(ui.End).Children(func() {
			if !hovered {
				return
			}
			b := widgets.Quiet(c.Key("edit-message")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Edit message")).Tooltip(tr("Edit and resend"))
			b.Children(func() { widgets.Icon(c, "edit", 14, widgets.Muted) })
			if b.Clicked() {
				app.StartEdit(m)
			}
		})
	})
}

// startEdit edits a message's text, without the attachment lines the
// composer added to it.
func (app *App) StartEdit(m appmodel.Message) {
	m.Content = appmodel.ClaudeCommandText(m.Content)
	var keep []string
	for _, l := range strings.Split(model.ImagePlaceholder.ReplaceAllString(m.Content, ""), "\n") {
		if !strings.HasPrefix(l, "📎 ") {
			keep = append(keep, l)
		}
	}
	body, _ := app.ThreadReferenceContent(strings.TrimSpace(strings.Join(keep, "\n")))
	app.Editing, app.EditText = m.ID, body
}

func (app *App) submitEdit(m appmodel.Message) {
	if strings.TrimSpace(app.EditText) == "" {
		return
	}
	app.Editing = 0
	app.C.Rewind(app.C.Current().ID, m.ID, app.EditText)
}

func (app *App) editMessageView(c *ui.Context, m appmodel.Message) {
	ui.Column(c).AlignSelf(ui.End).FillWidth().MaxWidth(560).Background(widgets.Bubble).Radius(16).Padding(12).Gap(8).Children(func() {
		in := app.ThreadInput(c.Key("input"), &app.editInput, app.EditText, strconv.Itoa(m.ID), tr("Edit message"), "", 13, 536, 1, 12, func(text string) { app.EditText = text }).AutoFocus()
		if in.Shortcut(0, ui.KeyEnter) {
			app.submitEdit(m)
		}
		if in.Shortcut(0, ui.KeyEscape) {
			app.Editing = 0
		}
		ui.Text(c, tr("Restart from this message. Later messages will be deleted. Miya will not undo file changes already made.")).FontSize(11).TextColor(widgets.Muted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				app.Editing = 0
			}
			if ui.PrimaryButton(c.Key("send"), tr("Send")).Disabled(strings.TrimSpace(app.EditText) == "").Clicked() {
				app.submitEdit(m)
			}
		})
	})
}
