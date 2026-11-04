package main

import (
	"os"
	"time"

	"github.com/egoist/mygo/ui"
)

// A new conversation has no desktop ID until sent arrives. Keep its first
// message outside the synchronized history so it cannot enter Have or caches.
type mobileNewSend struct {
	message Message
	thread  int
}

func (m *mobileApp) beginNewSend(text string, images []string) {
	m.clearNewSend()
	m.newSend = &mobileNewSend{message: Message{
		ID: -1, Role: "user", Content: text,
		Images: append([]string(nil), images...), SentAtMs: time.Now().UnixMilli(),
	}}
	m.view.groups = groupCache{}
}

func (m *mobileApp) clearNewSend() {
	if p := m.newSend; p != nil && p.thread != 0 {
		for _, path := range p.message.Images {
			go os.Remove(path)
		}
	}
	m.newSend = nil
}

func (m *mobileApp) failNewSend() {
	if m.newSend != nil && m.newSend.thread == 0 {
		m.newSend = nil
	}
}

func (m *mobileApp) receiveNewSend(t Thread) {
	if m.newSend == nil || m.newSend.thread == 0 || m.newSend.thread != t.ID {
		return
	}
	for _, message := range t.Messages {
		if message.Role == "user" {
			m.clearNewSend()
			return
		}
	}
}

func (m *mobileApp) newSendThread(cur Thread) (Thread, bool) {
	p := m.newSend
	if p == nil || p.thread != m.open || m.open != 0 && cur.ID != m.open {
		return Thread{}, false
	}
	if m.open == 0 {
		cur = Thread{ID: -1, Agent: m.newAgent}
	}
	cur.Messages = []Message{p.message}
	cur.Busy = false
	return cur, true
}

func (m *mobileApp) newSendStatus(c *ui.Context) {
	t := m.view.c.current()
	if m.newSend.thread != 0 && !t.Busy && !t.Loading && t.Status != "" {
		ui.Text(c, t.Status).FontSize(m.font(12)).TextColor(m.pal.warn).Padding(6)
		return
	}
	label := "正在同步对话…"
	if m.sending {
		label = "发送中…"
	}
	ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(8).Padding(6).Children(func() {
		ui.Spinner(c).FontSize(m.font(12)).TextColor(m.pal.muted)
		ui.Text(c, label).FontSize(m.font(12)).TextColor(m.pal.muted)
	})
}
