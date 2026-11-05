// Package phone is the background service's side of the phone connection
// that needs no controller: where its state lives, whether pairing is on,
// the message deltas it sends and the images a phone uploads.
package phone

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"miya/internal/appdir"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/remote"
	"miya/internal/svcproto"
)

// listThread is a conversation as the list shows it: no messages, and never
// the desktop's unsent draft.
func ListThread(t model.Thread) model.Thread {
	t.Messages = nil
	t.Draft = ""
	return t
}

func HasHistory(t model.Thread) bool { return len(t.Messages) > 0 || t.HistoryLoaded }

func Dir() string {
	if os.Getenv("MIYA_DIR") != "" || appdir.Isolated() {
		return filepath.Join(appdir.Dir(), "remote")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "quickgui-remote")
}

func prefsPath() string {
	if dir := Dir(); dir != "" {
		return filepath.Join(dir, "phone.json")
	}
	return ""
}

func LoadEnabled() bool {
	var prefs struct {
		Enabled bool `json:"enabled"`
	}
	data, err := os.ReadFile(prefsPath())
	return err == nil && json.Unmarshal(data, &prefs) == nil && prefs.Enabled
}

func PersistEnabled(enabled bool) {
	path := prefsPath()
	if path == "" || os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	data, _ := json.Marshal(map[string]bool{"enabled": enabled})
	_ = os.WriteFile(path, data, 0600)
}

// sameJSON compares two JSON values regardless of their spacing.
func SameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return string(a) == string(b)
	}
	return x.String() == y.String()
}

// phoneThreadSync is what one phone holds of the conversation it watches,
// as the service's windows have theirs (svcPeer.diff).
type ThreadSync struct {
	images map[string]bool
	sent   map[int]uint64
	order  []int
	meta   []byte
	fresh  bool
}

// newPhoneThreadSync starts from what the phone said it holds; without it
// the first update replaces the phone's copy.
func NewThreadSync(have []remote.Seen) *ThreadSync {
	s := &ThreadSync{sent: map[int]uint64{}, fresh: len(have) == 0}
	for _, h := range have {
		s.sent[h.ID] = h.FP
		s.order = append(s.order, h.ID)
	}
	return s
}

// update is the delta that brings the phone to t, and whether there is one;
// it records the delta as sent.
// HasImage reports whether the conversation last synced shows image src.
func (s *ThreadSync) HasImage(src string) bool { return s.images[src] }

func (s *ThreadSync) Update(t model.Thread) (phoneapi.ThreadDelta, bool) {
	msgs := t.Messages
	t.Messages, t.Draft = nil, ""
	meta, _ := json.Marshal(t)
	d := phoneapi.ThreadDelta{Thread: t, Reset: s.fresh, Order: make([]int, len(msgs))}
	if t.Loading && len(msgs) == 0 {
		// Resuming for a send can temporarily expose only thread metadata.
		// Keep the phone's history until the loaded messages replace it.
		d.Order = append([]int{}, s.order...)
		if !s.fresh && bytes.Equal(meta, s.meta) {
			return phoneapi.ThreadDelta{}, false
		}
		s.fresh, s.meta = false, meta
		return d, true
	}
	s.images = make(map[string]bool)
	for _, m := range msgs {
		for _, source := range m.Images {
			s.images[source] = true
		}
	}
	same := !s.fresh && len(msgs) == len(s.order)
	for i, m := range msgs {
		d.Order[i] = m.ID
		if same && s.order[i] != m.ID {
			same = false
		}
		if fp := svcproto.MessageFingerprint(m); s.sent[m.ID] != fp {
			s.sent[m.ID] = fp
			d.Changed = append(d.Changed, m)
		}
	}
	if same && len(d.Changed) == 0 && bytes.Equal(meta, s.meta) {
		return phoneapi.ThreadDelta{}, false
	}
	s.fresh, s.order, s.meta = false, d.Order, meta
	return d, true
}
