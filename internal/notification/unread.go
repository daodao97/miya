package notification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo/push/apns"

	"miya/internal/chat/model"
	"miya/internal/textutil"
)

type Unread struct {
	Key      string `json:"key"`
	Thread   string `json:"thread"`
	ServerID string `json:"serverId,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Project  string `json:"project,omitempty"`
	NoticeID string `json:"noticeId"`
	Version  uint64 `json:"version"`
}
type PhoneState struct {
	Revision uint64   `json:"revision"`
	Unread   []Unread `json:"unread"`
}
type deviceUnread struct {
	Revision uint64            `json:"revision"`
	Threads  map[string]Unread `json:"threads"`
}

func (n Notice) UnreadKey() string {
	if n.ServerID != "" {
		data, _ := json.Marshal([]string{n.Agent, n.Project, n.ServerID})
		return string(data)
	}
	return "thread:" + n.Thread
}
func (h *Hub) unreadDeviceLocked(id string) *deviceUnread {
	if h.unread == nil {
		h.unread = map[string]*deviceUnread{}
	}
	if h.unread[id] == nil {
		h.unread[id] = &deviceUnread{Threads: map[string]Unread{}}
	}
	if h.unread[id].Threads == nil {
		h.unread[id].Threads = map[string]Unread{}
	}
	return h.unread[id]
}
func (h *Hub) persistUnreadLocked() {
	if h.storageDir == "" {
		return
	}
	b, err := json.Marshal(h.unread)
	if err != nil {
		h.lastError = err.Error()
		return
	}
	if err = os.MkdirAll(h.storageDir, 0700); err == nil {
		path := filepath.Join(h.storageDir, "push-unread.json")
		if err = os.WriteFile(path+".tmp", b, 0600); err == nil {
			err = os.Rename(path+".tmp", path)
		}
	}
	if err != nil {
		h.lastError = "Failed to save unread status: " + err.Error()
	}
}
func (h *Hub) State(id string) PhoneState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.notificationStateLocked(id)
}
func (h *Hub) notificationStateLocked(id string) PhoneState {
	d := h.unreadDeviceLocked(id)
	out := PhoneState{Revision: d.Revision, Unread: make([]Unread, 0, len(d.Threads))}
	for _, u := range d.Threads {
		out.Unread = append(out.Unread, u)
	}
	sort.Slice(out.Unread, func(i, j int) bool { return out.Unread[i].Version < out.Unread[j].Version })
	return out
}
func (h *Hub) MarkRead(id, key string, version uint64) PhoneState {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.unreadDeviceLocked(id)
	if u, ok := d.Threads[key]; ok && version != 0 && u.Version <= version {
		delete(d.Threads, key)
		d.Revision++
		h.persistUnreadLocked()
		if r, ok := h.devices[id]; ok && !r.Disabled && (r.Token != "" || r.Live) {
			select {
			case h.queue <- pushDelivery{Registration: r, BadgeOnly: true}:
			default:
				h.lastError = "Notification queue is full"
			}
		}
	}
	return h.notificationStateLocked(id)
}

// Notification counts are absolute and recomputed at delivery time. A retry
// or a delayed alert can never restore an already-read conversation's badge.
func (h *Hub) notificationFor(d pushDelivery) (apns.Notification, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.devices[d.Registration.ID]
	if !ok || r.Disabled || r.Token != d.Registration.Token || r.Live != d.Registration.Live {
		return apns.Notification{}, false
	}
	state := h.unreadDeviceLocked(r.ID)
	count := len(state.Threads)
	current, unread := state.Threads[d.Notice.UnreadKey()]
	badgeOnly := d.BadgeOnly || !unread || current.Version != d.Version
	n := apns.Notification{DeviceToken: r.Token, Expiration: time.Now().Add(time.Hour), Payload: apns.Payload{Badge: &count}}
	if badgeOnly {
		n.CollapseID = "miya-badge"
		n.Payload.Silent = true
		return n, true
	}
	n.CollapseID = d.Notice.ID
	n.Payload.ID, n.Payload.Title, n.Payload.Body, n.Payload.Group = d.Notice.ID, d.Notice.Title, d.Notice.Body, "miya-tasks"
	n.Payload.Data = map[string]string{"thread": d.Notice.Thread, "serverId": d.Notice.ServerID, "agent": d.Notice.Agent, "project": d.Notice.Project, "revision": strconv.FormatUint(state.Revision, 10)}
	if r.Desktop != "" {
		n.Payload.Data["desktop"] = r.Desktop
	}
	return n, true
}

func (h *Hub) MarkAllRead(id string, version uint64) PhoneState {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.unreadDeviceLocked(id)
	changed := false
	for key, u := range d.Threads {
		if version != 0 && u.Version <= version {
			delete(d.Threads, key)
			changed = true
		}
	}
	if changed {
		d.Revision++
		h.persistUnreadLocked()
		if r, ok := h.devices[id]; ok && !r.Disabled && (r.Token != "" || r.Live) {
			select {
			case h.queue <- pushDelivery{Registration: r, BadgeOnly: true}:
			default:
				h.lastError = "Notification queue is full"
			}
		}
	}
	return h.notificationStateLocked(id)
}

// Matches reports whether u is t's unread state: by its backend session when
// it has one, else by its window-local ID.
func (u Unread) Matches(t model.Thread) bool {
	if u.ServerID != "" {
		return u.ServerID == t.ServerID && textutil.FirstNonEmpty(u.Agent, model.AgentCodex) == model.ThreadAgent(t)
	}
	return u.Thread == strconv.Itoa(t.ID)
}
