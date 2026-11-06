package main

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"miya/internal/remote"
)

func longThread(n int) Thread {
	t := Thread{ID: 7, ServerID: "srv-7", Title: "long", HistoryLoaded: true}
	for i := 1; i <= n; i++ {
		role := "assistant"
		if i%2 == 1 {
			role = "user"
		}
		t.Messages = append(t.Messages, Message{ID: i, Role: role, Content: fmt.Sprintf("Message %d. ", i) + strings.Repeat("Some markdown with `code` and a list.\n- item\n", 8), Status: "completed"})
	}
	return t
}

// phoneOf applies deltas as the phone does, from what it holds.
func phoneOf(t *testing.T, m *mobileApp, d phoneThreadDelta) []Message {
	t.Helper()
	data, _ := json.Marshal(d)
	var back phoneThreadDelta
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	m.open = back.Thread.ID
	m.applyThread(back)
	return m.viewer.current().Messages
}

func sameMessages(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

func TestPhoneThreadDeltas(t *testing.T) {
	desk := longThread(40)
	m := newMobileApp()
	s := newPhoneThreadSync(nil)

	d, ok := s.Update(desk)
	if !ok || !d.Reset || len(d.Changed) != 40 {
		t.Fatalf("first update should be whole: reset %v, %d changed", d.Reset, len(d.Changed))
	}
	if got := phoneOf(t, m, d); !sameMessages(got, desk.Messages) {
		t.Fatal("the phone should hold the conversation")
	}
	if _, ok := s.Update(desk); ok {
		t.Error("nothing changed, nothing to send")
	}

	// Streaming: the last message grows.
	desk.Messages[39].Content += " more"
	desk.Messages = append(desk.Messages, Message{ID: 41, Role: "assistant", Content: "new"})
	d, ok = s.Update(desk)
	if !ok || d.Reset || len(d.Changed) != 2 {
		t.Fatalf("a streamed update should send only what changed: %d changed", len(d.Changed))
	}
	if got := phoneOf(t, m, d); !sameMessages(got, desk.Messages) {
		t.Fatal("the phone should merge the delta")
	}

	// Reopening with what the phone holds sends no message.
	m.viewer.setActive(0)
	req := m.subscribeRequest(0)
	req.Thread = desk.ID
	for _, msg := range m.cachedMessages(desk.ServerID) {
		req.Have = append(req.Have, remote.Seen{ID: msg.ID, FP: messageFingerprint(msg)})
	}
	s = newPhoneThreadSync(req.Have)
	d, ok = s.Update(desk)
	if !ok || d.Reset || len(d.Changed) != 0 {
		t.Fatalf("a cached conversation should need no messages: reset %v, %d changed", d.Reset, len(d.Changed))
	}
	if got := phoneOf(t, m, d); !sameMessages(got, desk.Messages) {
		t.Fatal("the phone should rebuild from its cache")
	}

	// A restarted desktop numbers messages anew: everything is sent, and the
	// phone still ends with the right conversation.
	restarted := longThread(41)
	for i := range restarted.Messages {
		restarted.Messages[i].ID += 1000
	}
	restarted.Messages[39].Content = desk.Messages[39].Content
	restarted.Messages[40].Content = desk.Messages[40].Content
	s = newPhoneThreadSync(req.Have)
	d, _ = s.Update(restarted)
	if len(d.Changed) != 41 {
		t.Errorf("new IDs should resend everything: %d", len(d.Changed))
	}
	if got := phoneOf(t, m, d); !sameMessages(got, restarted.Messages) {
		t.Fatal("the phone should hold the restarted desktop's conversation")
	}
}

func TestPhoneThreadDeltasKeepHistoryWhileResuming(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		name := "same-connection"
		if reconnect {
			name = "reconnected"
		}
		t.Run(name, func(t *testing.T) {
			desk := longThread(4)
			m := newMobileApp()
			s := newPhoneThreadSync(nil)
			d, _ := s.Update(desk)
			phoneOf(t, m, d)
			if reconnect {
				var have []remote.Seen
				for _, msg := range desk.Messages {
					have = append(have, remote.Seen{ID: msg.ID, FP: messageFingerprint(msg)})
				}
				s = newPhoneThreadSync(have)
			}
			loading := listThread(desk)
			loading.Loading, loading.Busy = true, true
			for _, loaded := range []bool{false, true} {
				loading.HistoryLoaded = loaded
				d, _ = s.Update(loading)
				if len(d.Order) != len(desk.Messages) || len(d.Changed) != 0 || d.Reset {
					t.Fatalf("resume must retain message order: %+v", d)
				}
				if got := phoneOf(t, m, d); !sameMessages(got, desk.Messages) {
					t.Fatal("resuming erased the displayed history")
				}
			}
			// Returning history can renumber messages and add the new prompt.
			for i := range desk.Messages {
				desk.Messages[i].ID += 100
			}
			desk.Messages = append(desk.Messages, Message{ID: 105, Role: "user", Content: "Follow-up"})
			d, _ = s.Update(desk)
			if got := phoneOf(t, m, d); !sameMessages(got, desk.Messages) {
				t.Fatal("the loaded history and follow-up were not applied")
			}
			// An authoritative empty history still clears old messages.
			desk.Messages = nil
			d, _ = s.Update(desk)
			if got := phoneOf(t, m, d); len(got) != 0 {
				t.Fatal("completed empty history retained deleted messages")
			}
		})
	}
}

func compressed(frames [][]byte) int {
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.DefaultCompression)
	for _, f := range frames {
		w.Write(f)
		w.Flush()
	}
	return b.Len()
}

// TestPhoneTransferSize compares what a long conversation costs: opened,
// then streamed for 20 updates, then reopened.
func TestPhoneTransferSize(t *testing.T) {
	desk := longThread(300)
	var before, after [][]byte
	full := func() { b, _ := json.Marshal(desk); before = append(before, b) }
	s := newPhoneThreadSync(nil)
	delta := func() {
		if d, ok := s.Update(desk); ok {
			b, _ := json.Marshal(d)
			after = append(after, b)
		}
	}
	full()
	delta()
	openBefore, openAfter := len(before[0]), compressed(after)
	desk.Messages = append(desk.Messages, Message{ID: 301, Role: "assistant"})
	for i := 0; i < 20; i++ {
		desk.Messages[300].Content += "streamed words "
		full()
		delta()
	}
	var have []remote.Seen
	for _, m := range desk.Messages {
		have = append(have, remote.Seen{ID: m.ID, FP: messageFingerprint(m)})
	}
	full()
	s = newPhoneThreadSync(have)
	delta()
	sum := func(fs [][]byte) (n int) {
		for _, f := range fs {
			n += len(f)
		}
		return
	}
	t.Logf("open: before %d B, after %d B", openBefore, openAfter)
	t.Logf("open + 20 streamed + reopen: before %d B, after %d B uncompressed, %d B compressed", sum(before), sum(after), compressed(after))
	if compressed(after)*10 > sum(before) {
		t.Errorf("the new transfer should be far smaller")
	}
}
