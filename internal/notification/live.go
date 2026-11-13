package notification

import (
	"context"
	"encoding/json"

	"miya/internal/apns"
)

// LiveMessage is a notification sent to an Android phone over its paired
// connection (a "notice" frame): the same text, badge and routing data an
// APNs payload carries.
type LiveMessage struct {
	ID     string            `json:"id,omitempty"`
	Title  string            `json:"title,omitempty"`
	Body   string            `json:"body,omitempty"`
	Group  string            `json:"group,omitempty"`
	Badge  *int              `json:"badge,omitempty"`
	Silent bool              `json:"silent,omitempty"`
	Data   map[string]string `json:"data,omitempty"`
}

type liveSender struct {
	ctx  context.Context
	send func([]byte) bool
}

// AttachLive routes the live registration id's notifications to send until
// ctx ends: the phone session that registered it. A later session replaces
// an earlier one. Without a session, notifications for it are dropped; the
// phone reads its unread state when it connects again.
func (h *Hub) AttachLive(id string, ctx context.Context, send func([]byte) bool) {
	h.mu.Lock()
	if h.live == nil {
		h.live = map[string]liveSender{}
	}
	h.live[id] = liveSender{ctx: ctx, send: send}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		if l, ok := h.live[id]; ok && l.ctx == ctx {
			delete(h.live, id)
		}
		h.mu.Unlock()
	}()
}

// deliverLive sends the notification notificationFor built to the phone's
// open session, if there is one. It is not retried.
func (h *Hub) deliverLive(id string, n apns.Notification) {
	h.mu.Lock()
	l, ok := h.live[id]
	h.mu.Unlock()
	if !ok || l.ctx.Err() != nil {
		return
	}
	p := n.Payload
	b, err := json.Marshal(LiveMessage{ID: p.ID, Title: p.Title, Body: p.Body, Group: p.Group, Badge: p.Badge, Silent: p.Silent, Data: p.Data})
	if err == nil {
		l.send(b)
	}
}
