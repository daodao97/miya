package manager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"miya/internal/memory"
	"miya/internal/model"
	"strings"
	"time"
)

type Args struct {
	Query           string `json:"query,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         uint64 `json:"version,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Content         string `json:"content,omitempty"`
	TaskKey         string `json:"task_key,omitempty"`
	ValidUntil      string `json:"valid_until,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	Quote           string `json:"quote,omitempty"`
}

func Expiry(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errors.New("Expiration must use RFC3339, for example 2026-11-09T23:59:59+08:00")
	}
	return t, nil
}

func (mg *Manager) Source(t model.Thread, call model.Message, a Args) (memory.Source, error) {
	if !mg.Host.ThreadRef(t).Valid() {
		return memory.Source{}, errors.New("Session identity is not ready. Try again shortly.")
	}
	quote := strings.TrimSpace(a.Quote)
	if quote == "" {
		return memory.Source{}, errors.New("Provide an original excerpt of the user's explicit request to remember")
	}
	// Restrict evidence to the current user turn, before this tool call. It
	// cannot cite attachment/tool prose, assistant output or hidden context.
	end := len(t.Messages)
	for i, m := range t.Messages {
		if m.ID == call.ID {
			end = i
			break
		}
	}
	start := end
	for start > 0 {
		start--
		if t.Messages[start].Role == "user" {
			break
		}
	}
	var candidates []model.Message
	for _, m := range t.Messages[start:end] {
		if m.Role != "user" {
			continue
		}
		own, _ := model.SplitReferenceContext(m.Content)
		if a.SourceMessageID != "" && m.ItemID != a.SourceMessageID {
			continue
		}
		if strings.Contains(own, quote) {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) != 1 {
		return memory.Source{}, errors.New("The excerpt does not uniquely identify this turn's user message. Use the exact user text and source_message_id.")
	}
	m := candidates[0]
	at := m.SentAtMs
	if at == 0 {
		at = call.StartedAtMs
	}
	if at == 0 {
		at = time.Now().UnixMilli()
	}
	source := memory.Source{Thread: mg.Host.ThreadRef(t).URL(), TurnID: m.TurnID, Quote: quote, EvidenceKind: "user_statement", ObservedAt: time.UnixMilli(at)}
	if m.ItemID != "" && m.ItemID != "pending-user" {
		source.MessageID = m.ItemID
	} else {
		source.Limited = true
		if source.TurnID == "" {
			local := m.MemoryTurnID
			if local == "" {
				var b [16]byte
				if _, err := rand.Read(b[:]); err != nil {
					return memory.Source{}, err
				}
				local = hex.EncodeToString(b[:])
				mg.Host.UpdateThread(t.ID, func(t *model.Thread) {
					for i := range t.Messages {
						if t.Messages[i].ID == m.ID {
							t.Messages[i].MemoryTurnID = local
							break
						}
					}
				})
			}
			source.LocalTurnID = local
		}
		source.Locator = fmt.Sprintf("turn:%s;sent:%d;local-message:%d", m.TurnID, m.SentAtMs, m.ID)
	}
	return source, nil
}
