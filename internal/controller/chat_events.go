package controller

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	model "miya/internal/model"
	textutil "miya/internal/textutil"
	strings "strings"
	time "time"
)

func (c *Controller) Handle(f codex.Frame) {
	if len(f.ID) > 0 {
		c.ServerRequest(f)
		return
	}
	if f.Method == "account/updated" || f.Method == "account/rateLimits/updated" {
		c.quotaFrame(f)
		c.RefreshAccount()
		return
	}
	var e codex.Event
	if json.Unmarshal(f.Params, &e) != nil {
		return
	}
	if f.Method == "serverRequest/resolved" {
		c.removeRequest(e.RequestID)
		return
	}

	var target appmodel.Thread
	for _, t := range c.Threads() {
		if t.ServerID != "" && t.ServerID == e.ThreadID {
			target = t
			break
		}
	}
	if target.ID == 0 {
		for path, threads := range c.ProjectCache {
			if path == c.Cwd {
				continue
			}
			for _, t := range threads {
				if t.ServerID != "" && t.ServerID == e.ThreadID && model.ThreadAgent(t) == appmodel.AgentCodex {
					target = t
					break
				}
			}
			if target.ID != 0 {
				break
			}
		}
	}
	if target.ID == 0 {
		return
	}
	if e.Item.Type == "commandExecution" || e.Item.Type == "dynamicToolCall" || e.Item.Type == "mcpToolCall" || f.Method == "turn/started" || f.Method == "item/commandExecution/outputDelta" || f.Method == "item/commandExecution/terminalInteraction" || f.Method == "turn/completed" {
		c.noteRuntime(target)
	}
	completedMemory := false
	c.Update(target.ID, func(t *appmodel.Thread) {
		// Only fresh model output proves an upstream reconnection succeeded.
		// Commands may still produce output while the model stream retries.
		if t.Busy && strings.HasPrefix(t.Status, "Retrying: ") && (e.TurnID == "" || e.TurnID == t.TurnID) {
			switch f.Method {
			case "item/agentMessage/delta", "item/plan/delta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded":
				t.Status = "Working…"
			case "item/started", "item/completed":
				switch e.Item.Type {
				case "agentMessage", "reasoning", "plan":
					t.Status = "Working…"
				}
			}
		}
		switch f.Method {
		case "thread/goal/updated":
			t.GoalRevision++
			appmodel.ApplyCodexGoal(t, e.Goal)
		case "thread/goal/cleared":
			t.GoalRevision++
			appmodel.ApplyCodexGoal(t, nil)
		case "thread/closed":
			appmodel.ResetCodexSession(t)
			t.Status = ""
		case "thread/name/updated", "thread/updated":
			if strings.TrimSpace(e.Name) != "" {
				t.Title = strings.TrimSpace(e.Name)
			}
		case "turn/started":
			if t.TurnID != e.Turn.ID {
				appmodel.ResetTurnOutput(t)
			}
			now := time.Now().UnixMilli()
			t.LastActivityAt = now
			t.TurnID = e.Turn.ID
			t.Busy = true
			t.Status = "Working…"
			t.StartedAtMs = now
			if start, _ := appmodel.CodexTurnTimes(e.Turn); start > 0 {
				t.StartedAtMs = start
			}
			t.CompletedAtMs = 0
		case "item/agentMessage/delta", "item/plan/delta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded", "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
			role, kind := "assistant", "agentMessage"
			switch f.Method {
			case "item/plan/delta":
				role, kind = "activity", "plan"
			case "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded":
				role, kind = "activity", "reasoning"
			case "item/commandExecution/outputDelta":
				role, kind = "tool", "commandExecution"
			case "item/fileChange/outputDelta":
				role, kind = "tool", "fileChange"
			}
			if f.Method == "item/reasoning/summaryPartAdded" {
				e.Delta = "\n"
			}

			for i := range t.Messages {
				if t.Messages[i].ItemID == e.ItemID && t.Messages[i].TurnID == e.TurnID {
					if t.Messages[i].Content == "Thinking…" {
						t.Messages[i].Content = ""
					}
					t.Messages[i].Content += e.Delta
					if role == "tool" {
						t.Messages[i].Content = textutil.Bounded(t.Messages[i].Content)
					}
					return
				}
			}
			now := time.Now().UnixMilli()
			t.Messages = append(t.Messages, appmodel.Message{ID: c.NextID(), Role: role, Kind: kind, Status: "inProgress", Content: e.Delta, ItemID: e.ItemID, TurnID: e.TurnID, SentAtMs: now, StartedAtMs: now})
		case "item/started", "item/completed":
			if f.Method == "item/completed" && strings.EqualFold(e.Item.Type, "contextCompaction") {
				t.MemoryEpoch++
			}
			if e.Item.Type == "userMessage" {
				for i := range t.Messages {
					// The server sends started and completed as separate events.
					// Reconcile both with the optimistic composer message; otherwise
					// every normal send is rendered twice as a user bubble.
					if t.Messages[i].ItemID == "pending-user" || t.Messages[i].ItemID == e.Item.ID {
						t.Messages[i].ItemID = e.Item.ID
						t.Messages[i].TurnID = e.TurnID
						return
					}
				}
			}
			m, ok := appmodel.ItemMessage(e.Item)
			if !ok {
				return
			}
			if m.Status == "" {
				if f.Method == "item/completed" {
					m.Status = "completed"
				} else {
					m.Status = "inProgress"
				}
			}
			m.ItemID = e.Item.ID
			m.TurnID = e.TurnID
			now := time.Now().UnixMilli()
			if m.SentAtMs == 0 {
				m.SentAtMs = now
			}
			if m.StartedAtMs == 0 {
				m.StartedAtMs = now
			}
			for i := range t.Messages {
				if t.Messages[i].ItemID == m.ItemID && t.Messages[i].TurnID == m.TurnID {
					if f.Method == "item/completed" || t.Messages[i].Content == "" {
						m.ID = t.Messages[i].ID
						t.Messages[i] = m
					}
					return
				}
			}
			m.ID = c.NextID()
			t.Messages = append(t.Messages, m)
		case "turn/plan/updated":
			content := e.Explanation
			for _, step := range e.Plan {
				content += "\n" + step.Status + " · " + step.Step
			}
			c.upsert(t, appmodel.Message{Role: "activity", Kind: "plan", Plan: e.Plan, Content: strings.TrimSpace(content), ItemID: "turn-plan", TurnID: e.TurnID})
		case "turn/diff/updated":
			stats := model.CountDiffLines(e.Diff)
			c.upsert(t, appmodel.Message{Role: "tool", Kind: "diff", Content: textutil.Bounded(e.Diff), DiffStats: &stats, ItemID: "turn-diff", TurnID: e.TurnID})
		case "thread/tokenUsage/updated":
			if e.TokenUsage != nil {
				if e.TurnID == "" || e.TurnID == t.TurnID {
					appmodel.RecordCodexOutput(t, e.TokenUsage)
				}
				t.Usage = e.TokenUsage
				t.Tokens = e.TokenUsage.Last.TotalTokens
			}
		case "turn/completed":
			if t.TurnID != "" && t.TurnID != e.Turn.ID {
				return
			}
			completedMemory = e.Turn.Status == "completed" && !t.StopRequested && e.Turn.Error == nil
			t.CompletedAtMs = time.Now().UnixMilli()
			start, end := appmodel.CodexTurnTimes(e.Turn)
			if start > 0 {
				t.StartedAtMs = start
			}
			if end > 0 {
				t.CompletedAtMs = end
			}
			for i := range t.Messages {
				m := &t.Messages[i]
				if m.TurnID == e.Turn.ID && m.Status == "inProgress" {
					m.Status = e.Turn.Status
				}
				if m.TurnID == e.Turn.ID {
					if t.StartedAtMs > 0 {
						m.StartedAtMs = t.StartedAtMs
					}
					m.CompletedAtMs = t.CompletedAtMs
				}
			}
			t.Busy = false
			t.TurnID = ""
			t.StopRequested = false
			t.Status = ""
			if e.Turn.Status == "interrupted" {
				t.Status = "Stopped"
			}
			if e.Turn.Status == "failed" {
				t.Status = "Turn failed"
				// A terminal error can precede completion, whose payload may
				// omit it. Keep that specific error rather than losing its detail.
				for _, m := range t.Messages {
					if m.ItemID == "turn-error" && m.TurnID == e.Turn.ID {
						t.Status = m.Content
						break
					}
				}
			}
			if e.Turn.Error != nil {
				t.Status = e.Turn.Error.Message
				c.upsert(t, appmodel.Message{Role: "error", Kind: "error", Content: e.Turn.Error.Message, TurnID: e.Turn.ID, ItemID: "turn-error", Status: "failed"})
			} else if e.Turn.Status == "failed" {
				c.upsert(t, appmodel.Message{Role: "error", Kind: "error", Content: t.Status, TurnID: e.Turn.ID, ItemID: "turn-error", Status: "failed"})
			}
			c.clearApprovals(e.ThreadID, e.Turn.ID)
		case "error":
			if e.TurnID != "" && t.TurnID != "" && e.TurnID != t.TurnID {
				return
			}
			if e.Error != nil {
				t.Status = e.Error.Message
				if e.WillRetry {
					t.Status = "Retrying: " + t.Status
				} else {
					turnID := e.TurnID
					if turnID == "" {
						turnID = t.TurnID
					}
					c.upsert(t, appmodel.Message{Role: "error", Kind: "error", Content: e.Error.Message, TurnID: turnID, ItemID: "turn-error", Status: "failed"})
				}
			}
		}
	})
	if completedMemory {
		c.Memories().CompletedTurn(target.ID, e.Turn.ID)
	}
	c.ManagedCodexOutput(target, f.Method, e)
}

func (c *Controller) clearApprovals(thread, turn string) {
	out := []codex.Approval{}
	for _, a := range c.Approvals() {
		if a.ThreadID != thread || a.TurnID != turn {
			out = append(out, a)
		}
	}
	c.SetApprovals(out)
}

func (c *Controller) upsert(t *appmodel.Thread, m appmodel.Message) {
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
