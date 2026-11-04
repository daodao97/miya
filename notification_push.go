package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/push/apns"
)

type pushRegistration struct {
	ID       string    `json:"id"`
	Token    string    `json:"token"`
	Disabled bool      `json:"disabled"`
	Updated  time.Time `json:"updated"`
}
type pushDelivery struct {
	Version      uint64
	BadgeOnly    bool
	Registration pushRegistration
	Notice       taskNotice
	Attempts     int
}
type pushHub struct {
	storageDir   string
	unread       map[string]*deviceUnread
	desktopUntil time.Time
	mu           sync.Mutex
	devices      map[string]pushRegistration
	queue        chan pushDelivery
	lastError    string
}

func newPushHub() *pushHub {
	h := &pushHub{storageDir: serviceDir(), devices: map[string]pushRegistration{}, queue: make(chan pushDelivery, 128)}
	if b, e := os.ReadFile(filepath.Join(serviceDir(), "push-devices.json")); e == nil {
		_ = json.Unmarshal(b, &h.devices)
	}
	if h.devices == nil {
		h.devices = map[string]pushRegistration{}
	}
	if b, e := os.ReadFile(filepath.Join(h.storageDir, "push-unread.json")); e == nil {
		_ = json.Unmarshal(b, &h.unread)
	}
	go h.run()
	return h
}
func (h *pushHub) register(r pushRegistration) error {
	if len(r.ID) != 32 {
		return errors.New("invalid device ID")
	}
	if _, e := hex.DecodeString(r.ID); e != nil {
		return errors.New("invalid device ID")
	}
	if !r.Disabled {
		if len(r.Token) < 32 || len(r.Token) > 512 || len(r.Token)%2 != 0 {
			return errors.New("invalid APNs token")
		}
		if _, e := hex.DecodeString(r.Token); e != nil {
			return errors.New("invalid APNs token")
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.devices) >= 32 {
		if _, ok := h.devices[r.ID]; !ok {
			return errors.New("notification device limit reached")
		}
	}
	r.Updated = time.Now()
	h.devices[r.ID] = r
	d := h.unreadDeviceLocked(r.ID)
	if r.Disabled && len(d.Threads) > 0 {
		d.Threads = map[string]phoneUnread{}
		d.Revision++
		h.persistUnreadLocked()
	}
	b, e := json.Marshal(h.devices)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(serviceDir(), 0700); e != nil {
		return e
	}
	path := filepath.Join(serviceDir(), "push-devices.json")
	if e = os.WriteFile(path+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func (h *pushHub) publish(n taskNotice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Now().Before(h.desktopUntil) {
		return
	}
	for _, r := range h.devices {
		if r.Disabled || time.Since(r.Updated) > 90*24*time.Hour {
			continue
		}
		d := h.unreadDeviceLocked(r.ID)
		key := n.unreadKey()
		if previous, ok := d.Threads[key]; ok && previous.NoticeID == n.ID {
			continue
		}
		d.Revision++
		d.Threads[key] = phoneUnread{Key: key, Thread: n.Thread, ServerID: n.ServerID, Agent: n.Agent, Project: n.Project, NoticeID: n.ID, Version: d.Revision}
		h.persistUnreadLocked()
		select {
		case h.queue <- pushDelivery{Registration: r, Notice: n, Version: d.Revision}:
		default:
			h.lastError = "Notification queue is full"
		}
	}
}
func (h *pushHub) run() {
	for d := range h.queue {
		notice, ok := h.notificationFor(d)
		if !ok {
			continue
		}
		p, e := apns.OpenProvider(serviceDir())
		if e == nil && p.Topic() != "com.daodao.miya" {
			e = errors.New("APNs topic must be com.daodao.miya")
		}
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
