package controller

import (
	"context"
	"time"

	"miya/internal/appmodel"
	"miya/internal/codex"
	"miya/internal/composer"
	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
)

// codexBackend talks to the local Codex app-server.
type CodexBackend struct{}

func (c *Controller) resetCodexSessions() {
	for _, t := range c.AllThreads() {
		if model.ThreadAgent(t) == appmodel.AgentCodex && t.ExecutionID != "" {
			c.ManagedEvent(t.ExecutionID, dg.Event{State: dg.NeedsAttention, Result: dg.Result{Error: "Codex connection ended before execution could be confirmed"}})
		}
	}
	for path, threads := range c.ProjectCache {
		threads = append([]model.Thread(nil), threads...)
		for i := range threads {
			model.ResetCodexSession(&threads[i])
		}
		c.ProjectCache[path] = threads
	}
	threads := append([]model.Thread(nil), c.Threads()...)
	for i := range threads {
		model.ResetCodexSession(&threads[i])
	}
	c.SetThreads(threads)
	if c.ProjectCache != nil && c.Cwd != "" {
		c.ProjectCache[c.Cwd] = append([]model.Thread(nil), threads...)
	}
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}

func (CodexBackend) ID() string { return appmodel.AgentCodex }

func (CodexBackend) Name() string { return model.AgentName(model.AgentCodex) }

func (CodexBackend) Icon() appmodel.AgentIconSpec {
	return appmodel.AgentIconSpec{PNG: appmodel.CodexAgentPNG}
}

func (CodexBackend) Caps() agentCaps {
	return agentCaps{Models: true, Effort: true, Quota: true, ThreadCommands: true}
}

// Discover connects, which lists the workspace's threads once connected.
func (CodexBackend) Discover(c *Controller) { c.Connect() }

func (CodexBackend) Ready(c *Controller) bool { return c.Ready() }

func (CodexBackend) Problem(c *Controller) (string, bool) {
	if !c.Ready() {
		return tr(c.Status()), true
	}
	return "", false
}

func (CodexBackend) Reconnect(c *Controller) { c.Connect() }

func (CodexBackend) Open(c *Controller, t model.Thread) {
	if (!t.HistoryLoaded || t.ReadOnly) && t.ServerID != "" && !t.Loading && !t.Busy && c.Client != nil {
		c.readHistory(t)
	}
}

func (CodexBackend) Send(c *Controller, req model.SendRequest) int { return c.sendCodex(req) }

func (CodexBackend) Stop(c *Controller, t model.Thread) {
	// Without a turn yet, the run interrupts itself once turn/start returns.
	if t.TurnID != "" {
		c.interrupt(t)
	}
}

func (CodexBackend) Archive(c *Controller, t model.Thread) { c.archiveCodexThread(t) }

func (CodexBackend) Delete(c *Controller, t model.Thread) { c.deleteCodexThread(t) }

func (CodexBackend) Suspend(*Controller, model.Thread) {}

func (CodexBackend) Owns(a codex.Approval) bool { return !claudeApproval(a) }

func (CodexBackend) Respond(c *Controller, a codex.Approval, result any) {
	client := c.Client
	c.removeRequest(a.ID)
	if client == nil {
		return
	}
	go func() {
		if err := client.Reply(a.ID, result); err != nil {
			c.Dispatch(func() { c.SetStatus("Response failed: " + err.Error()) })
		}
	}()
}

func (CodexBackend) Settings() []AgentSetting { return nil }

// Codex rewinds with thread/revert, which drops a turn and every later one
// from the thread's history. Local file changes stay as they are.
func (CodexBackend) CanRewind(c *Controller, t model.Thread, m model.Message) bool {
	return m.TurnID != "" && t.ServerID != "" && c.Ready()
}

func (CodexBackend) Rewind(c *Controller, t model.Thread, m model.Message, text string) {
	client, cwd, policy := c.Client, c.ThreadWorkspace(t.ID), c.permissionForThread(t)
	parent := c.Ctx
	if parent == nil {
		parent = context.Background()
	}
	uiruntime.GoAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		var err error
		if !t.Resumed {
			err = client.Call(ctx, "thread/resume", map[string]any{"threadId": t.ServerID, "cwd": cwd, "sandbox": prefs.SandboxForPolicy(policy), "approvalPolicy": prefs.ApprovalForPolicy(policy), "approvalsReviewer": prefs.ReviewerForPolicy(policy)}, nil)
		}
		if err == nil {
			err = client.Call(ctx, "thread/revert", map[string]any{"threadId": t.ServerID, "beforeTurnId": m.TurnID}, nil)
		}
		c.Dispatch(func() {
			c.finishRewind(t.ID, m.ID, text, err, func(t *model.Thread) { t.Resumed, t.HistoryLoaded = true, true })
		})
	})
}

// Codex steers with turn/steer, which the running turn takes as input. The
// user message shows at once and is matched as a sent one is; a steer Codex
// refuses, as for a turn that just ended, goes to the queue or is sent.
func (CodexBackend) Steer(c *Controller, t model.Thread, req model.SendRequest) bool {
	if t.TurnID == "" || t.ServerID == "" || !t.Resumed || c.Client == nil {
		return false
	}
	client, cwd := c.Client, c.ThreadWorkspace(t.ID)
	content, images := msgfmt.AttachmentDisplay(req.Text, req.Attachments)
	sentAt := time.Now().UnixMilli()
	pending := c.NextID()
	localTurn := c.Memories().BeginTurn(t, req, pending)
	c.Update(t.ID, func(t *model.Thread) {
		t.LastActivityAt = sentAt
		t.Messages = append(t.Messages, model.Message{ID: pending, Role: "user", Content: content, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, ItemID: "pending-user", SentAtMs: sentAt, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
	})
	go func() {
		mem := c.Memories().PrepareInput(t, req, cwd, c.ThreadRef(t).URL())
		input := []map[string]any{{"type": "text", "text": mem.Text(req.Text), "text_elements": []any{}}}
		own, _ := model.SplitReferenceContext(req.Text)
		input = append(input, composer.FileMentionInputs(own, cwd)...)
		input = append(input, c.AttachmentInputs(req.Attachments)...)
		params := map[string]any{"threadId": t.ServerID, "expectedTurnId": t.TurnID, "input": input}
		ctx, cancel := context.WithTimeout(c.parentContext(), 30*time.Second)
		defer cancel()
		err := client.Call(ctx, "turn/steer", params, nil)
		mem.Finish(err == nil)
		if err == nil {
			return
		}
		c.Dispatch(func() {
			c.Update(t.ID, func(t *model.Thread) {
				for i, m := range t.Messages {
					if m.ID == pending {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
			})
			retry := req
			retry.Steer = false
			if c.Submit(retry) == 0 {
				c.ReturnDraft(t.ID, req.Text, req.Attachments)
			}
		})
	}()
	return true
}
