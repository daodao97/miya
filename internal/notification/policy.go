package notification

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Phone notices learn from how they were received. A learnable notice (one
// with a Category that is not Urgent) that the person clears with "mark all
// as read" without opening its conversation is a dismissal; LearnAfter
// dismissals in a row of one category make a rule that sends that category
// as a badge only. While a category is quiet, opening its conversations
// LearnAfter times in a row lifts the rule. Opening a banner, or dismissing
// a badge, ends the opposite streak.
//
// Urgent notices (approvals, reviews, failures, results that need the
// person) never learn and are never quieted by a rule. Quiet hours send
// every notice as a badge only and teach nothing.
const LearnAfter = 2

// QuietHours are local times HH:MM; Start == End is never quiet.
type QuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// Rule is a learned category. Name is the project or automation name it
// is about, never translated; Kind is the part of Category before ":".
type Rule struct {
	Category  string    `json:"category"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name,omitempty"`
	LearnedAt time.Time `json:"learnedAt"`
}

// PolicyView is what settings show and edit.
type PolicyView struct {
	Quiet QuietHours `json:"quiet"`
	Rules []Rule     `json:"rules"`
}

type categoryState struct {
	Name      string    `json:"name,omitempty"`
	Dismissed int       `json:"dismissed,omitempty"`
	Opened    int       `json:"opened,omitempty"`
	Last      string    `json:"last,omitempty"`
	Silent    bool      `json:"silent,omitempty"`
	LearnedAt time.Time `json:"learnedAt,omitzero"`
}

type policy struct {
	Quiet      QuietHours                `json:"quiet"`
	Categories map[string]*categoryState `json:"categories,omitempty"`
}

// DefaultQuietHours are offered when quiet hours are first turned on.
var DefaultQuietHours = QuietHours{Start: "22:00", End: "08:00"}

func clockMinutes(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// Quiet reports whether now falls in the quiet hours.
func (q QuietHours) Quiet(now time.Time) bool {
	start, ok1 := clockMinutes(q.Start)
	end, ok2 := clockMinutes(q.End)
	if !q.Enabled || !ok1 || !ok2 || start == end {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

// delivery says how a notice is sent: as a banner, as a badge because its
// category learned to be quiet, or as a badge in quiet hours.
type delivery int

const (
	deliverBanner delivery = iota
	deliverSilenced
	deliverQuietHours
)

func (h *Hub) now() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

func (h *Hub) deliveryLocked(n Notice) delivery {
	if h.policy.Quiet.Quiet(h.now()) {
		return deliverQuietHours
	}
	if n.learnable() {
		if st := h.policy.Categories[n.Category]; st != nil && st.Silent {
			return deliverSilenced
		}
	}
	return deliverBanner
}

func (n Notice) learnable() bool { return n.Category != "" && !n.Urgent }

func (h *Hub) categoryLocked(category string) *categoryState {
	if h.policy.Categories == nil {
		h.policy.Categories = map[string]*categoryState{}
	}
	st := h.policy.Categories[category]
	if st == nil {
		st = &categoryState{}
		h.policy.Categories[category] = st
	}
	return st
}

// reactLocked counts how the person received u. Each notice counts once,
// whichever phone or the desktop reports it first.
func (h *Hub) reactLocked(u Unread, opened bool) {
	if u.Category == "" || !u.Banner && !u.Silenced {
		return
	}
	st := h.categoryLocked(u.Category)
	if st.Last == u.NoticeID {
		return
	}
	st.Last = u.NoticeID
	switch {
	case u.Banner && opened:
		st.Dismissed = 0
	case u.Banner:
		st.Dismissed++
		st.Opened = 0
		if st.Dismissed >= LearnAfter && !st.Silent {
			st.Silent, st.LearnedAt, st.Dismissed = true, h.now(), 0
		}
	case opened:
		st.Opened++
		if st.Opened >= LearnAfter && st.Silent {
			st.Silent, st.LearnedAt, st.Opened = false, time.Time{}, 0
		}
	default:
		st.Opened = 0
	}
	h.persistPolicyLocked()
}

// NoteOpenedElsewhere counts a conversation the person read on the desktop
// as opened, so clearing its phone badge later is not a dismissal. key is
// the desktop unread key, which has no project.
func (h *Hub) NoteOpenedElsewhere(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := false
	for _, d := range h.unread {
		if d == nil {
			continue
		}
		for k, u := range d.Threads {
			if (Notice{Thread: u.Thread, ServerID: u.ServerID, Agent: u.Agent}).UnreadKey() != key || !u.Banner && !u.Silenced {
				continue
			}
			h.reactLocked(u, true)
			u.Banner, u.Silenced = false, false
			d.Threads[k] = u
			changed = true
		}
	}
	if changed {
		h.persistUnreadLocked()
	}
}

// Policy returns the quiet hours and the learned rules, newest first.
func (h *Hub) Policy() PolicyView {
	h.mu.Lock()
	defer h.mu.Unlock()
	view := PolicyView{Quiet: h.policy.Quiet, Rules: []Rule{}}
	if view.Quiet.Start == "" {
		view.Quiet.Start, view.Quiet.End = DefaultQuietHours.Start, DefaultQuietHours.End
	}
	for category, st := range h.policy.Categories {
		if st.Silent {
			kind, _, _ := strings.Cut(category, ":")
			view.Rules = append(view.Rules, Rule{Category: category, Kind: kind, Name: st.Name, LearnedAt: st.LearnedAt})
		}
	}
	sort.Slice(view.Rules, func(i, j int) bool {
		if !view.Rules[i].LearnedAt.Equal(view.Rules[j].LearnedAt) {
			return view.Rules[i].LearnedAt.After(view.Rules[j].LearnedAt)
		}
		return view.Rules[i].Category < view.Rules[j].Category
	})
	return view
}

// SetQuietHours replaces the quiet hours.
func (h *Hub) SetQuietHours(q QuietHours) error {
	if _, ok := clockMinutes(q.Start); !ok {
		return errors.New("Quiet hours need start and end times as HH:MM")
	}
	if _, ok := clockMinutes(q.End); !ok {
		return errors.New("Quiet hours need start and end times as HH:MM")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.policy.Quiet = q
	return h.persistPolicyLocked()
}

// ForgetRule sends a learned category as banners again and starts its
// count over.
func (h *Hub) ForgetRule(category string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.policy.Categories[category]; !ok {
		return errors.New("No such notification rule")
	}
	delete(h.policy.Categories, category)
	return h.persistPolicyLocked()
}

func (h *Hub) loadPolicy() {
	if h.storageDir == "" {
		return
	}
	if b, err := os.ReadFile(filepath.Join(h.storageDir, "push-policy.json")); err == nil {
		_ = json.Unmarshal(b, &h.policy)
	}
}

func (h *Hub) persistPolicyLocked() error {
	if h.storageDir == "" {
		return nil
	}
	b, err := json.Marshal(h.policy)
	if err == nil {
		err = os.MkdirAll(h.storageDir, 0700)
	}
	if err == nil {
		path := filepath.Join(h.storageDir, "push-policy.json")
		if err = os.WriteFile(path+".tmp", b, 0600); err == nil {
			err = os.Rename(path+".tmp", path)
		}
	}
	if err != nil {
		h.lastError = "Failed to save notification rules: " + err.Error()
	}
	return err
}

// PolicyRequest changes the quiet hours, or forgets a learned rule.
type PolicyRequest struct {
	Quiet  *QuietHours `json:"quiet,omitempty"`
	Forget string      `json:"forget,omitempty"`
}
