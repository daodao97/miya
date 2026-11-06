package mobile

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/remote"
	"miya/internal/widgets"
)

func (m *App) queueAction(op string, id int) {
	if m.Conn == nil {
		m.SendErr = m.disconnectedSendError()
		return
	}
	r := m.ThreadRequest(op, m.Open)
	r.QueuedID = id
	m.sendPhoneAction(r)
}

func (m *App) sendPhoneAction(r remote.Request) {
	conn, gen := m.Conn, m.Generation
	if conn == nil {
		m.SendErr = m.disconnectedSendError()
		return
	}
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() { m.SendErr = tr("Action failed: ") + err.Error() })
		}
	}()
}

func (m *App) queueView(c *ui.Context) {
	t := m.Viewer.Current()
	if len(t.Queue) == 0 {
		return
	}
	p := m.Pal
	ui.Column(c.Key("queue")).FillWidth().Background(p.surface).Radius(16).Padding(10).Gap(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, fmt.Sprintf(tr("%d messages queued"), len(t.Queue))).FontSize(m.Font(13)).Grow(1)
			if !t.Busy && t.StopRequested && m.pill(c, "resume", tr("Continue sending"), false).Clicked() {
				m.queueAction("drain-queue", 0)
			}
		})
		ui.Scroll(c).FillWidth().MaxHeight(150).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for _, q := range t.Queue {
					ui.Row(c.Key(q.ID)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						text := strings.Join(strings.Fields(appmodel.ThreadContextDisplay(q.Text)), " ")
						if len(q.Attachments) > 0 {
							text += fmt.Sprintf(tr(" · %d attachments"), len(q.Attachments))
						}
						ui.Text(c, text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(m.Font(13))
						if t.Busy {
							b := m.phoneButton(c.Key("steer")).Size(36, 36).Justify(ui.Center).Label(tr("Steer queued message now"))
							b.Children(func() { widgets.Icon(c, "arrow-up", 16, p.ink) })
							if b.Clicked() {
								m.queueAction("steer-queued", q.ID)
							}
						}
						b := m.phoneButton(c.Key("remove")).Size(36, 36).Justify(ui.Center).Label(tr("Delete queued message"))
						b.Children(func() { widgets.Icon(c, "x", 16, p.muted) })
						if b.Clicked() {
							m.queueAction("dequeue", q.ID)
						}
					})
				}
			})
		})
	})
}
