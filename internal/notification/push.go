package notification

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"miya/internal/apns"
	"miya/internal/appdir"
)

const APNsTopic = "com.daodao.miya"

// OpenAPNsProvider checks that the sender is configured for Miya.
func OpenAPNsProvider(dir string) (*apns.Provider, error) {
	p, err := apns.OpenProvider(dir)
	if err != nil {
		return nil, err
	}
	if p.Topic() != APNsTopic {
		return nil, fmt.Errorf("APNs topic must be %s", APNsTopic)
	}
	return p, nil
}

type Registration struct {
	ID       string `json:"id"`
	Token    string `json:"token"`
	Disabled bool   `json:"disabled"`
	// Desktop is the phone's opaque ID for this desktop. Notifications echo
	// it so a phone paired with several desktops opens the right one.
	Desktop string `json:"desktop,omitempty"`
	// Live is an Android phone that receives notifications over its paired
	// connection while that is open (see AttachLive); it has no token.
	Live    bool      `json:"live,omitempty"`
	Updated time.Time `json:"updated"`
}
type pushDelivery struct {
	Version      uint64
	BadgeOnly    bool
	Registration Registration
	Notice       Notice
	Attempts     int
}
type Hub struct {
	storageDir   string
	unread       map[string]*deviceUnread
	desktopUntil time.Time
	mu           sync.Mutex
	devices      map[string]Registration
	queue        chan pushDelivery
	lastError    string
	live         map[string]liveSender
	policy       policy
	// clock is injectable for tests of quiet hours.
	clock func() time.Time
}

func NewHub() *Hub {
	h := &Hub{storageDir: appdir.Dir(), devices: map[string]Registration{}, queue: make(chan pushDelivery, 128)}
	if b, e := os.ReadFile(filepath.Join(appdir.Dir(), "push-devices.json")); e == nil {
		_ = json.Unmarshal(b, &h.devices)
	}
	if h.devices == nil {
		h.devices = map[string]Registration{}
	}
	if b, e := os.ReadFile(filepath.Join(h.storageDir, "push-unread.json")); e == nil {
		_ = json.Unmarshal(b, &h.unread)
	}
	h.loadPolicy()
	go h.run()
	return h
}
func (h *Hub) Register(r Registration) error {
	if len(r.ID) != 32 {
		return errors.New("invalid device ID")
	}
	if _, e := hex.DecodeString(r.ID); e != nil {
		return errors.New("invalid device ID")
	}
	if r.Live && r.Token != "" {
		return errors.New("invalid live registration")
	}
	if !r.Disabled && !r.Live {
		if len(r.Token) < 32 || len(r.Token) > 512 || len(r.Token)%2 != 0 {
			return errors.New("invalid APNs token")
		}
		if _, e := hex.DecodeString(r.Token); e != nil {
			return errors.New("invalid APNs token")
		}
	}
	if len(r.Desktop) > 64 {
		return errors.New("invalid desktop ID")
	}
	if _, e := hex.DecodeString(r.Desktop); e != nil {
		return errors.New("invalid desktop ID")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	unreadChanged := false
	if !r.Disabled {
		// A phone that lost its device ID registers again under a new one,
		// with the APNs token it had: its old registration would deliver
		// every notification twice and keep a badge of its own.
		for id, old := range h.devices {
			if id != r.ID && r.Token != "" && old.Token == r.Token {
				delete(h.devices, id)
				if _, ok := h.unread[id]; ok {
					delete(h.unread, id)
					unreadChanged = true
				}
			}
		}
	}
	if len(h.devices) >= 32 {
		if _, ok := h.devices[r.ID]; !ok {
			return errors.New("notification device limit reached")
		}
	}
	r.Updated = time.Now()
	h.devices[r.ID] = r
	d := h.unreadDeviceLocked(r.ID)
	if r.Disabled && len(d.Threads) > 0 {
		d.Threads = map[string]Unread{}
		d.Revision++
		unreadChanged = true
	}
	if unreadChanged {
		h.persistUnreadLocked()
	}
	b, e := json.Marshal(h.devices)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(appdir.Dir(), 0700); e != nil {
		return e
	}
	path := filepath.Join(appdir.Dir(), "push-devices.json")
	if e = os.WriteFile(path+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func (h *Hub) Publish(n Notice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Now().Before(h.desktopUntil) {
		return
	}
	mode := h.deliveryLocked(n)
	// Only notices sent by the rules teach them; quiet hours teach nothing.
	category := ""
	if n.learnable() && mode != deliverQuietHours {
		category = n.Category
		if st := h.categoryLocked(category); n.Topic != "" && st.Name != n.Topic {
			st.Name = n.Topic
			h.persistPolicyLocked()
		}
	}
	for _, r := range h.devices {
		if r.Disabled || time.Since(r.Updated) > 90*24*time.Hour {
			continue
		}
		d := h.unreadDeviceLocked(r.ID)
		key := n.UnreadKey()
		if previous, ok := d.Threads[key]; ok && previous.NoticeID == n.ID {
			continue
		}
		d.Revision++
		d.Threads[key] = Unread{Key: key, Thread: n.Thread, ServerID: n.ServerID, Agent: n.Agent, Project: n.Project, NoticeID: n.ID, Version: d.Revision,
			Category: category, Banner: category != "" && mode == deliverBanner, Silenced: category != "" && mode == deliverSilenced}
		h.persistUnreadLocked()
		select {
		case h.queue <- pushDelivery{Registration: r, Notice: n, Version: d.Revision, BadgeOnly: mode != deliverBanner}:
		default:
			h.lastError = "Notification queue is full"
		}
	}
}
func (h *Hub) run() {
	for d := range h.queue {
		notice, ok := h.notificationFor(d)
		if !ok {
			continue
		}
		if d.Registration.Live {
			h.deliverLive(d.Registration.ID, notice)
			continue
		}
		p, e := OpenAPNsProvider(h.storageDir)
		if e == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, e = p.Send(ctx, notice)
			cancel()
		}
		h.mu.Lock()
		if e != nil {
			h.lastError = e.Error()
		} else {
			h.lastError = ""
		}
		h.mu.Unlock()
		if e != nil && d.Attempts < 3 {
			d.Attempts++
			time.AfterFunc(time.Duration(d.Attempts)*10*time.Second, func() {
				select {
				case h.queue <- d:
				default:
				}
			})
		}
	}
}

// noteDesktopPresence holds phone notifications until until: a desktop
// window in use shows them itself.
func (h *Hub) NoteDesktopPresence(until time.Time) {
	h.mu.Lock()
	h.desktopUntil = until
	h.mu.Unlock()
}

// NewQueueHub is a hub for tests outside this package: it keeps devices in
// memory and queues deliveries for NextDelivery instead of sending them.
func NewQueueHub(dir string, devices ...Registration) *Hub {
	h := &Hub{storageDir: dir, devices: map[string]Registration{}, queue: make(chan pushDelivery, 20)}
	h.loadPolicy()
	for _, d := range devices {
		h.devices[d.ID] = d
	}
	return h
}

// NextDelivery waits for the next queued delivery and returns the APNs
// notification it makes.
func (h *Hub) NextDelivery() (apns.Notification, bool) { return h.notificationFor(<-h.queue) }
