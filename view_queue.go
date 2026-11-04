package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// queueView lists the messages waiting for the running turn to end.
func (app *chatApp) queueView(c *ui.Context, t Thread) {
	if len(t.Queue) == 0 {
		return
	}
	paused := !t.Busy && t.StopRequested
	ui.Column(c.Key("queue")).Gap(4).Padding(8, 10).Background(subtle).Border(1, line).Radius(12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			icon(c, "clock", 13, muted)
			title := fmt.Sprintf(tr("%d queued · Sent together when the current turn ends"), len(t.Queue))
			if paused {
				title = fmt.Sprintf(tr("%d queued · Sending paused"), len(t.Queue))
			}
			mutedText(c, title).Grow(1).MinWidth(0)
			if paused {
				quiet(c.Key("resume-queue")).Height(24).Children(func() { ui.Text(c, tr("Continue sending")) }).OnClick(func() { app.c.resumeQueue(t.ID) })
			}
		})
		for i, q := range t.Queue {
			ui.Row(c.Key(q.ID)).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, fmt.Sprintf("%d.", i+1)).FontSize(12).TextColor(muted)
				text := strings.Join(strings.Fields(threadContextDisplay(q.Text)), " ")
				if n := len(q.Attachments); n > 0 {
					text += fmt.Sprintf(" · 📎 %d", n)
				}
				ui.Text(c, text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12)
				if t.Busy && !t.Loading {
					now := quiet(c.Key("steer")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Steer now")).Tooltip(tr("Insert message into the current turn"))
					now.Children(func() { icon(c, "arrow-up", 13, muted) })
					now.OnClick(func() { app.c.steerQueued(t.ID, q.ID) })
				}
				edit := quiet(c.Key("edit")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Edit queued message")).Tooltip(tr("Move to input to edit"))
				edit.Children(func() { icon(c, "edit", 13, muted) })
				edit.OnClick(func() { app.editQueued(t.ID, q) })
				x := quiet(c.Key("remove")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Delete queued message")).Tooltip(tr("Delete"))
				x.Children(func() { icon(c, "x", 13, muted) })
				x.OnClick(func() { app.c.removeQueued(t.ID, q.ID) })
			})
		}
	})
}

// editQueued takes a message out of the queue and back into the composer,
// before what is already being written there.
func (app *chatApp) editQueued(threadID int, q queuedMessage) {
	cc := app.c
	cc.removeQueued(threadID, q.ID)
	text, _ := splitReferenceContext(q.Text)
	if draft := strings.TrimSpace(cc.draft()); draft != "" {
		text += "\n" + draft
	}
	cc.rememberDraft(text)
	cc.addAttachments(q.Attachments)
}
