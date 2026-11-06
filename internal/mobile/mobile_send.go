package mobile

import (
	"os"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
)

// A new conversation has no desktop ID until sent arrives. Keep its first
// message outside the synchronized history so it cannot enter Have or caches.
type mobileNewSend struct {
	Message model.Message
	thread  int
}

func (m *App) BeginNewSend(text string, images []string) {
	m.clearNewSend()
	m.NewSend = &mobileNewSend{Message: model.Message{
		ID: -1, Role: "user", Content: text,
		Images: append([]string(nil), images...), SentAtMs: time.Now().UnixMilli(),
	}}
	m.View.Groups = conversation.GroupCache{}
}

func (m *App) clearNewSend() {
	if p := m.NewSend; p != nil && p.thread != 0 {
		for _, path := range p.Message.Images {
			go os.Remove(path)
		}
	}
	m.NewSend = nil
}

func (m *App) failNewSend() {
	if m.NewSend != nil && m.NewSend.thread == 0 {
		m.NewSend = nil
	}
}

func (m *App) receiveNewSend(t model.Thread) {
	if m.NewSend == nil || m.NewSend.thread == 0 || m.NewSend.thread != t.ID {
		return
	}
	for _, message := range t.Messages {
		if message.Role == "user" {
			m.clearNewSend()
			return
		}
	}
}

func (m *App) NewSendThread(cur model.Thread) (model.Thread, bool) {
	p := m.NewSend
	if p == nil || p.thread != m.Open || m.Open != 0 && cur.ID != m.Open {
		return model.Thread{}, false
	}
	if m.Open == 0 {
		cur = model.Thread{ID: -1, Agent: m.NewAgent}
	}
	cur.Messages = []model.Message{p.Message}
	cur.Busy = false
	return cur, true
}

func (m *App) newSendStatus(c *ui.Context) {
	t := m.Viewer.Current()
	if m.NewSend.thread != 0 && !t.Busy && !t.Loading && t.Status != "" {
		ui.Text(c, t.Status).FontSize(m.Font(12)).TextColor(m.Pal.warn).Padding(6)
		return
	}
	label := tr("Syncing conversation…")
	if m.Sending {
		label = tr("Sending…")
	}
	ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(8).Padding(6).Children(func() {
		ui.Spinner(c).FontSize(m.Font(12)).TextColor(m.Pal.muted)
		ui.Text(c, label).FontSize(m.Font(12)).TextColor(m.Pal.muted)
	})
}
