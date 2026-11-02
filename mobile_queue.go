package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

func (m *mobileApp) queueAction(op string, id int) {
	if m.conn == nil {
		m.sendErr = m.disconnectedSendError()
		return
	}
	r := m.threadRequest(op, m.open)
	r.QueuedID = id
	m.sendPhoneAction(r)
}
func (m *mobileApp) sendPhoneAction(r remote.Request) {
	conn, gen := m.conn, m.generation
	if conn == nil {
		m.sendErr = m.disconnectedSendError()
		return
	}
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() { m.sendErr = "操作失败：" + err.Error() })
		}
	}()
}
func (m *mobileApp) queueView(c *ui.Context) {
	t := m.view.c.current()
	if len(t.Queue) == 0 {
		return
	}
	p := m.pal
	ui.Column(c.Key("queue")).FillWidth().Background(p.surface).Radius(16).Padding(10).Gap(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, fmt.Sprintf("待发送 %d 条", len(t.Queue))).FontSize(m.font(13)).Grow(1)
			if !t.Busy && t.StopRequested && m.pill(c, "resume", "继续发送", false).Clicked() {
				m.queueAction("drain-queue", 0)
			}
		})
		ui.Scroll(c).FillWidth().MaxHeight(150).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for _, q := range t.Queue {
					ui.Row(c.Key(q.ID)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						text := strings.Join(strings.Fields(threadContextDisplay(q.Text)), " ")
						if len(q.Attachments) > 0 {
							text += fmt.Sprintf(" · %d 个附件", len(q.Attachments))
						}
						ui.Text(c, text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(m.font(13))
						if t.Busy {
							b := m.phoneButton(c.Key("steer")).Size(36, 36).Justify(ui.Center).Label("立即引导排队消息")
							b.Children(func() { icon(c, "arrow-up", 16, p.ink) })
							if b.Clicked() {
								m.queueAction("steer-queued", q.ID)
							}
						}
						b := m.phoneButton(c.Key("remove")).Size(36, 36).Justify(ui.Center).Label("删除排队消息")
						b.Children(func() { icon(c, "x", 16, p.muted) })
						if b.Clicked() {
							m.queueAction("dequeue", q.ID)
						}
					})
				}
			})
		})
	})
}
