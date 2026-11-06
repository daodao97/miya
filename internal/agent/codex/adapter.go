package codex

import (
	"context"
	"time"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	codex "miya/internal/agent/codex/native"
	codexpolicy "miya/internal/agent/codex/policy"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/interaction"
	"miya/internal/uiruntime"
)

func (b Adapter) ID() string { return agentmeta.Codex }

func (b Adapter) Name() string { return model.AgentName(model.AgentCodex) }

func (b Adapter) Icon() agentmeta.Icon         { return agentmeta.For(b.ID()).Icon }
func (b Adapter) Caps() agentmeta.Capabilities { return agentmeta.For(b.ID()).Capabilities }

// Discover connects, which lists the workspace's threads once connected.
func (b Adapter) Discover() { b.host.Connect() }

func (b Adapter) Ready() bool { return b.host.Snapshot().Ready }

func (b Adapter) Problem() (string, bool) {
	c := b.host
	if !c.Snapshot().Ready {
		return tr(c.Snapshot().Status), true
	}
	return "", false
}

func (b Adapter) Reconnect() { b.host.Connect() }

func (b Adapter) Open(t model.Thread) {
	c := b.host
	if (!t.HistoryLoaded || t.ReadOnly) && t.ServerID != "" && !t.Loading && !t.Busy && c.Snapshot().Client != nil {
		b.Resume(t, nil)
	}
}

func (b Adapter) Stop(t model.Thread) {
	// Without a turn yet, the run interrupts itself once turn/start returns.
	if t.TurnID != "" {
		b.Interrupt(t)
	}
}

func (b Adapter) Suspend(model.Thread) {}

func (b Adapter) RespondNative(a codex.Approval, result any) {
	c := b.host
	client := c.Snapshot().Client
	c.RemoveRequest(a.ID)
	if client == nil {
		return
	}
	go func() {
		if err := client.Reply(a.ID, result); err != nil {
			c.Dispatch(func() { c.SetStatus("Response failed: " + err.Error()) })
		}
	}()
}

func (b Adapter) Settings() []agent.Setting { return nil }

// Codex rewinds with thread/revert, which drops a turn and every later one
// from the thread's history. Local file changes stay as they are.
func (b Adapter) CanRewind(t model.Thread, m model.Message) bool {
	c := b.host
	return m.TurnID != "" && t.ServerID != "" && c.Snapshot().Ready
}

func (b Adapter) Rewind(t model.Thread, m model.Message, text string) {
	c := b.host
	client, cwd, policy := c.Snapshot().Client, c.ThreadWorkspace(t.ID), c.PermissionForThread(t)
	parent := c.Context()
	if parent == nil {
		parent = context.Background()
	}
	uiruntime.GoAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		var err error
		if !t.Resumed {
			err = client.Call(ctx, "thread/resume", map[string]any{"threadId": t.ServerID, "cwd": cwd, "sandbox": codexpolicy.SandboxForPolicy(policy), "approvalPolicy": codexpolicy.ApprovalForPolicy(policy), "approvalsReviewer": codexpolicy.ReviewerForPolicy(policy)}, nil)
		}
		if err == nil {
			err = client.Call(ctx, "thread/revert", map[string]any{"threadId": t.ServerID, "beforeTurnId": m.TurnID}, nil)
		}
		c.Dispatch(func() {
			c.FinishRewind(t.ID, m.ID, text, err, func(t *model.Thread) { t.Resumed, t.HistoryLoaded = true, true })
		})
	})
}

// Codex steers with turn/steer, which the running turn takes as input. The
// user message shows at once and is matched as a sent one is; a steer Codex
// refuses, as for a turn that just ended, goes to the queue or is sent.
func (b Adapter) Steer(t model.Thread, req model.SendRequest) bool {
	c := b.host
	if t.TurnID == "" || t.ServerID == "" || !t.Resumed || c.Snapshot().Client == nil {
		return false
	}
	client, cwd := c.Snapshot().Client, c.ThreadWorkspace(t.ID)
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
		ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
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

func (b Adapter) Reply(a interaction.Request, reply interaction.Response) {
	b.host.Respond(a, codex.EncodeResponse(a, reply))
}
