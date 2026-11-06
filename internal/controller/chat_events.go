package controller

import (
	codex "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
)

func (c *Controller) Handle(f codex.Frame) { c.codexAdapter().Handle(f) }

func (c *Controller) upsert(t *model.Thread, m model.Message) {
	if m.ExecutionID == "" {
		m.ExecutionID = t.ExecutionID
	}
	for i := range t.Messages {
		if t.Messages[i].ItemID == m.ItemID && t.Messages[i].TurnID == m.TurnID {
			m.ID = t.Messages[i].ID
			t.Messages[i] = m
			return
		}
	}
	m.ID = c.NextID()
	t.Messages = append(t.Messages, m)
}
