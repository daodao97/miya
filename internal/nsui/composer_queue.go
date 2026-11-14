package nsui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func queueView(cx *core.Scope, c Host) core.Node {
	thread := Live(c.Current)
	return core.Show{When: func() bool { return len(thread().Queue) > 0 }, Then: func(cx *core.Scope) core.Node {
		paused := func() bool { t := thread(); return !t.Busy && t.StopRequested }
		return core.View{TestID: "queued-messages", StyleFunc: nswidgets.CardStyle(cx), Children: []core.Node{
			settingsActions(core.Text{ContentFunc: func() string {
				pattern := tr("%d queued · Sent together when the current turn ends")
				if paused() {
					pattern = tr("%d queued · Sending paused")
				}
				return fmt.Sprintf(pattern, len(thread().Queue))
			}}, core.Show{When: paused, Then: func(cx *core.Scope) core.Node {
				return ui.Button{Text: tr("Continue sending"), TestID: "resume-queue", OnPress: func() { c.ResumeQueue(c.Current().ID) }}
			}}),
			core.Each(func() []model.QueuedMessage { return thread().Queue }, func(q model.QueuedMessage) int { return q.ID }, func(cx *core.Scope, q func() model.QueuedMessage, index func() int) core.Node {
				id := strconv.Itoa(core.Untracked(cx, func() int { return q().ID }))
				return settingsActions(core.Text{TestID: "queue-text-" + id, ContentFunc: func() string {
					text := strings.Join(strings.Fields(appmodel.ThreadContextDisplay(q().Text)), " ")
					if n := len(q().Attachments); n > 0 {
						text += fmt.Sprintf(" · 📎 %d", n)
					}
					return fmt.Sprintf("%d. %s", index()+1, text)
				}, MaxLines: 1, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)}},
					core.Show{When: func() bool { t := thread(); return t.Busy && !t.Loading && !t.ReadOnly }, Then: func(cx *core.Scope) core.Node {
						return ui.IconButton{TestID: "queue-steer-" + id, Label: tr("Steer now"), Icon: nswidgets.IconOf("arrow-up"), OnPress: func() { c.SteerQueued(c.Current().ID, q().ID) }}
					}},
					ui.IconButton{TestID: "queue-edit-" + id, Label: tr("Edit queued message"), Icon: nswidgets.IconOf("edit"), Disabled: func() bool { return thread().ReadOnly }, OnPress: func() { editQueued(c, c.Current().ID, q()) }},
					ui.IconButton{TestID: "queue-remove-" + id, Label: tr("Delete queued message"), Icon: nswidgets.IconOf("x"), Disabled: func() bool { return thread().ReadOnly }, OnPress: func() { c.RemoveQueued(c.Current().ID, q().ID) }},
				)
			}),
		}}
	}}
}
func editQueued(c Host, threadID int, q model.QueuedMessage) {
	c.RemoveQueued(threadID, q.ID)
	text, _ := model.SplitReferenceContext(q.Text)
	if f := c.State().Draft; f != nil && strings.TrimSpace(f()) != "" {
		text += "\n" + strings.TrimSpace(f())
	}
	c.RememberDraft(text)
	c.AddAttachments(q.Attachments)
}
