package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegisterLive(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	h := &Hub{storageDir: t.TempDir(), devices: map[string]Registration{}, queue: make(chan pushDelivery, 4)}
	if err := h.Register(Registration{ID: strings.Repeat("1", 32), Live: true, Token: strings.Repeat("ab", 32)}); err == nil {
		t.Fatal("live registration with a token accepted")
	}
	for _, id := range []string{strings.Repeat("1", 32), strings.Repeat("2", 32)} {
		if err := h.Register(Registration{ID: id, Live: true}); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.devices) != 2 {
		t.Fatalf("two live phones without tokens replaced each other: %d devices", len(h.devices))
	}
}

func TestLiveDelivery(t *testing.T) {
	r := Registration{ID: strings.Repeat("a", 32), Live: true, Desktop: "0f", Updated: time.Now()}
	h := NewQueueHub(t.TempDir(), r)
	got := make(chan []byte, 4)
	ctx, cancel := context.WithCancel(context.Background())
	h.AttachLive(r.ID, ctx, func(b []byte) bool { got <- b; return true })
	go h.run()

	h.Publish(Notice{ID: "n1", Thread: "7", Title: "Done", Body: "Tests pass"})
	select {
	case b := <-got:
		var m LiveMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m.ID != "n1" || m.Title != "Done" || m.Body != "Tests pass" || m.Silent || m.Badge == nil || *m.Badge != 1 ||
			m.Data["thread"] != "7" || m.Data["desktop"] != "0f" {
			t.Fatalf("message %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no live delivery")
	}

	h.MarkAllRead(r.ID, h.State(r.ID).Revision)
	select {
	case b := <-got:
		var m LiveMessage
		_ = json.Unmarshal(b, &m)
		if !m.Silent || m.Badge == nil || *m.Badge != 0 {
			t.Fatalf("badge update %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no badge update after reading")
	}

	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		n := len(h.live)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ended session still attached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.Publish(Notice{ID: "n2", Thread: "8"})
	select {
	case b := <-got:
		t.Fatalf("delivered without a session: %s", b)
	case <-time.After(200 * time.Millisecond):
	}
}
