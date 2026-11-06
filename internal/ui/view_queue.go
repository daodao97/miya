package desktopui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/widgets"
)

// queueView lists the messages waiting for the running turn to end.
func (app *App) queueView(c *ui.Context, t model.Thread) {
	if len(t.Queue) == 0 {
		return
	}
	paused := !t.Busy && t.StopRequested
	ui.Column(c.Key("queue")).Gap(4).Padding(8, 10).Background(widgets.Subtle).Border(1, widgets.Line).Radius(12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			widgets.Icon(c, "clock", 13, widgets.Muted)
			title := fmt.Sprintf(tr("%d queued · Sent together when the current turn ends"), len(t.Queue))
			if paused {
				title = fmt.Sprintf(tr("%d queued · Sending paused"), len(t.Queue))
			}
			widgets.MutedText(c, title).Grow(1).MinWidth(0)
			if paused {
				widgets.Quiet(c.Key("resume-queue")).Height(24).Children(func() { ui.Text(c, tr("Continue sending")) }).OnClick(func() { app.C.ResumeQueue(t.ID) })
			}
		})
		for i, q := range t.Queue {
			ui.Row(c.Key(q.ID)).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, fmt.Sprintf("%d.", i+1)).FontSize(12).TextColor(widgets.Muted)
				text := strings.Join(strings.Fields(appmodel.ThreadContextDisplay(q.Text)), " ")
				if n := len(q.Attachments); n > 0 {
					text += fmt.Sprintf(" · 📎 %d", n)
				}
				ui.Text(c, text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12)
				if t.Busy && !t.Loading {
					now := widgets.Quiet(c.Key("steer")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Steer now")).Tooltip(tr("Insert message into the current turn"))
					now.Children(func() { widgets.Icon(c, "arrow-up", 13, widgets.Muted) })
					now.OnClick(func() { app.C.SteerQueued(t.ID, q.ID) })
				}
				edit := widgets.Quiet(c.Key("edit")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Edit queued message")).Tooltip(tr("Move to input to edit"))
				edit.Children(func() { widgets.Icon(c, "edit", 13, widgets.Muted) })
				edit.OnClick(func() { app.editQueued(t.ID, q) })
				x := widgets.Quiet(c.Key("remove")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Delete queued message")).Tooltip(tr("Delete"))
				x.Children(func() { widgets.Icon(c, "x", 13, widgets.Muted) })
				x.OnClick(func() { app.C.RemoveQueued(t.ID, q.ID) })
			})
		}
	})
}

// editQueued takes a message out of the queue and back into the composer,
// before what is already being written there.
func (app *App) editQueued(threadID int, q model.QueuedMessage) {
	cc := app.C
	cc.RemoveQueued(threadID, q.ID)
	text, _ := model.SplitReferenceContext(q.Text)
	if draft := strings.TrimSpace(cc.State().Draft()); draft != "" {
		text += "\n" + draft
	}
	cc.RememberDraft(text)
	cc.AddAttachments(q.Attachments)
}
