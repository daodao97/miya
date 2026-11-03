package main

import (
	"context"
	_ "embed"
	"errors"
	"strings"
	"time"

	"miya/internal/codex"
)

//go:embed assets/agents/codex.png
var codexAgentPNG []byte

// codexBackend talks to the local Codex app-server.
type codexBackend struct{}

// Resumed and writer ownership are valid only for the current app-server.
// Claude's Resumed instead describes its persisted CLI session.
func resetCodexSession(t *Thread) {
	if threadAgent(*t) != agentCodex {
		return
	}
	t.Resumed, t.ReadOnly = false, false
	t.Loaded, t.Loading, t.Busy, t.StopRequested = false, false, false, false
	t.TurnID = ""
	t.MemoryEpoch++
	if t.Status == "Working…" || t.Status == "Starting…" || t.Status == "Stopping…" {
		t.Status = ""
	}
}

func (c *chatController) resetCodexSessions() {
	for path, threads := range c.projectCache {
		threads = append([]Thread(nil), threads...)
		for i := range threads {
			resetCodexSession(&threads[i])
		}
		c.projectCache[path] = threads
	}
	threads := append([]Thread(nil), c.threads()...)
	for i := range threads {
		resetCodexSession(&threads[i])
	}
	c.setThreads(threads)
	if c.projectCache != nil && c.cwd != "" {
		c.projectCache[c.cwd] = append([]Thread(nil), threads...)
	}
	if c.revision != nil {
		c.setRevision(c.revision() + 1)
	}
}

func isMissingCodexThread(err error, threadID string) bool {
	var rpc *codex.RPCError
	return errors.As(err, &rpc) && rpc.Code == -32600 &&
		strings.TrimSpace(rpc.Message) == "thread not found: "+threadID
}

func (codexBackend) ID() string          { return agentCodex }
func (codexBackend) Name() string        { return "Codex" }
func (codexBackend) Icon() agentIconSpec { return agentIconSpec{PNG: codexAgentPNG} }
func (codexBackend) Caps() agentCaps {
	return agentCaps{Models: true, Effort: true, Quota: true, ThreadCommands: true}
}

// Discover connects, which lists the workspace's threads once connected.
func (codexBackend) Discover(c *chatController)   { c.connect() }
func (codexBackend) Ready(c *chatController) bool { return c.ready() }

func (codexBackend) Problem(c *chatController) (string, bool) {
	if !c.ready() {
		return tr(c.status()), true
	}
	return "", false
}

func (codexBackend) Reconnect(c *chatController) { c.connect() }

func (codexBackend) Open(c *chatController, t Thread) {
	if (!t.HistoryLoaded || t.ReadOnly) && t.ServerID != "" && !t.Loading && !t.Busy && c.client != nil {
		c.readHistory(t)
	}
}

func (codexBackend) Send(c *chatController, req sendRequest) int { return c.sendCodex(req) }

func (codexBackend) Stop(c *chatController, t Thread) {
	// Without a turn yet, the run interrupts itself once turn/start returns.
	if t.TurnID != "" {
		c.interrupt(t)
	}
}

func (codexBackend) Archive(c *chatController, t Thread) { c.archiveCodexThread(t) }
func (codexBackend) Delete(c *chatController, t Thread)  { c.deleteCodexThread(t) }
func (codexBackend) Suspend(*chatController, Thread)     {}

func (codexBackend) Owns(a codex.Approval) bool { return !claudeApproval(a) }

func (codexBackend) Respond(c *chatController, a codex.Approval, result any) {
	client := c.client
	c.removeRequest(a.ID)
	if client == nil {
		return
	}
	go func() {
		if err := client.Reply(a.ID, result); err != nil {
			c.dispatch(func() { c.setStatus("Response failed: " + err.Error()) })
		}
	}()
}

func (codexBackend) Settings() []agentSetting { return nil }

// codexAccountLimitID names the account-wide rate limit bucket; other
// buckets are a model's own.
const codexAccountLimitID = "codex"

// Codex rewinds with thread/revert, which drops a turn and every later one
// from the thread's history. Local file changes stay as they are.
func (codexBackend) CanRewind(c *chatController, t Thread, m Message) bool {
	return m.TurnID != "" && t.ServerID != "" && c.ready()
}

func (codexBackend) Rewind(c *chatController, t Thread, m Message, text string) {
	client, cwd, policy := c.client, c.threadWorkspace(t.ID), c.permissionForThread(t)
	parent := c.ctx
	if parent == nil {
		parent = context.Background()
	}
	goAsync(func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		var err error
		if !t.Resumed {
			err = client.Call(ctx, "thread/resume", map[string]any{"threadId": t.ServerID, "cwd": cwd, "sandbox": sandboxForPermissionPolicy(policy), "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy)}, nil)
		}
		if err == nil {
			err = client.Call(ctx, "thread/revert", map[string]any{"threadId": t.ServerID, "beforeTurnId": m.TurnID}, nil)
		}
		c.dispatch(func() {
			c.finishRewind(t.ID, m.ID, text, err, func(t *Thread) { t.Resumed, t.HistoryLoaded = true, true })
		})
	})
}

// Codex steers with turn/steer, which the running turn takes as input. The
// user message shows at once and is matched as a sent one is; a steer Codex
// refuses, as for a turn that just ended, goes to the queue or is sent.
func (codexBackend) Steer(c *chatController, t Thread, req sendRequest) bool {
	if t.TurnID == "" || t.ServerID == "" || !t.Resumed || c.client == nil {
		return false
	}
	client, cwd := c.client, c.threadWorkspace(t.ID)
	content, images := attachmentDisplay(req.Text, req.Attachments)
	sentAt := time.Now().UnixMilli()
	pending := c.nextID()
	localTurn := c.beginMemoryTurn(t, req, pending)
	c.update(t.ID, func(t *Thread) {
		t.LastActivityAt = sentAt
		t.Messages = append(t.Messages, Message{ID: pending, Role: "user", Content: content, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, ItemID: "pending-user", SentAtMs: sentAt, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
	})
	go func() {
		mem := c.prepareMemoryInput(t, req, cwd, c.threadRef(t).URL())
		input := []map[string]any{{"type": "text", "text": mem.text(req.Text), "text_elements": []any{}}}
		own, _ := splitReferenceContext(req.Text)
		input = append(input, fileMentionInputs(own, cwd)...)
		input = append(input, c.attachmentInputs(req.Attachments)...)
		params := map[string]any{"threadId": t.ServerID, "expectedTurnId": t.TurnID, "input": input}
		ctx, cancel := context.WithTimeout(c.parentContext(), 30*time.Second)
		defer cancel()
		err := client.Call(ctx, "turn/steer", params, nil)
		mem.finish(err == nil)
		if err == nil {
			return
		}
		c.dispatch(func() {
			c.update(t.ID, func(t *Thread) {
				for i, m := range t.Messages {
					if m.ID == pending {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
			})
			retry := req
			retry.Steer = false
			if c.submit(retry) == 0 {
				c.returnDraft(t.ID, req.Text, req.Attachments)
			}
		})
	}()
	return true
}
