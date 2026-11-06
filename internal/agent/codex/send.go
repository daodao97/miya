package codex

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentmeta "miya/internal/agent/catalog"
	codex "miya/internal/agent/codex/native"
	codexpolicy "miya/internal/agent/codex/policy"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	dg "miya/internal/delegation"
	"miya/internal/prefs"
	"miya/internal/textutil"
)

func (b Adapter) Send(req model.SendRequest) int {
	c := b.host
	if !c.Snapshot().Ready {
		return 0
	}
	text, attachments := req.Text, req.Attachments
	t := c.ThreadWithID(req.ThreadID)
	if t.ServerID != "" && !t.Resumed {
		b.Resume(t, &req)
		return t.ID
	}
	if t.ID == 0 {
		// Use the first request until the agent supplies a named title.
		project := textutil.FirstNonEmpty(req.Project, c.Cwd())
		t = model.Thread{PermissionPolicy: req.Policy, ID: c.NextID(), Title: msgfmt.ConversationTitle("", text, ""), Project: filepath.Base(project), Age: "now", Loaded: true, HistoryLoaded: true}
		c.AddThread(project, t)
	}
	id := t.ID
	client := c.Snapshot().Client
	if client == nil {
		c.Update(id, func(t *model.Thread) {
			t.Busy = false
			t.Status = "Codex is reconnecting…"
		})
		c.ManagedEvent(req.ExecutionID, dg.Event{State: dg.Failed, Result: dg.Result{Error: "Codex is not connected before submission"}})
		return id
	}
	cwd := c.ThreadWorkspace(id)
	modelID, effort, policy := req.Model, req.Effort, req.Policy
	serviceTier := prefs.Load("codexServiceTier")
	sentAt := time.Now().UnixMilli()
	pendingUserID := c.NextID()
	localTurn := c.Memories().BeginTurn(t, req, pendingUserID)
	c.Update(id, func(t *model.Thread) {
		msgfmt.ResetTurnOutput(t)
		t.ExecutionID = req.ExecutionID
		if !req.KeepSessionSettings {
			t.LaunchModel, t.LaunchEffort = req.Model, req.Effort
		}
		t.Busy = true
		t.LastActivityAt = sentAt
		t.Status = "Starting…"
		t.Draft = ""
		t.StopRequested = false
		content, images := msgfmt.AttachmentDisplay(text, attachments)
		t.Messages = append(t.Messages, model.Message{ID: pendingUserID, Role: "user", Content: content, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, ItemID: "pending-user", SentAtMs: sentAt, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
	})
	go func() {
		ctx, cancel := context.WithTimeout(c.Context(), 45*time.Second)
		defer cancel()
		serverID := t.ServerID
		var err error
		missingThread := false
		mayHaveStarted := false
		if serverID == "" {
			var r codex.ThreadResponse
			params := map[string]any{"cwd": cwd, "sandbox": codexpolicy.SandboxForPolicy(policy), "approvalPolicy": codexpolicy.ApprovalForPolicy(policy), "approvalsReviewer": codexpolicy.ReviewerForPolicy(policy)}
			if modelID != "" {
				params["model"] = modelID
			}
			err = client.Call(ctx, "thread/start", params, &r)
			if err == nil {
				serverID = r.Thread.ID
				if serverID == "" {
					err = fmt.Errorf("Codex returned an empty thread ID")
				} else {
					sid := serverID
					serverName := strings.TrimSpace(r.Thread.Name)
					c.Dispatch(func() {
						c.Update(id, func(t *model.Thread) {
							t.ServerID = sid
							t.Resumed = true
							if serverName != "" {
								t.Title = serverName
							}
						})
					})
				}
			}
		}
		var result struct {
			Turn codex.Turn `json:"turn"`
		}
		if err == nil {
			err = c.BindExecution(ctx, req.ExecutionID, c.ThreadRef(model.Thread{Agent: agentmeta.Codex, ServerID: serverID}).URL(), "")
		}
		if err == nil {
			mem := c.Memories().PrepareInput(t, req, cwd, c.ThreadRef(model.Thread{ServerID: serverID}).URL())
			input := []map[string]any{{"type": "text", "text": composer.CollaborationTaggedInput(mem.Text(text), req.Parts), "text_elements": []any{}}}
			own, _ := model.SplitReferenceContext(text)
			input = append(input, composer.FileMentionInputs(own, cwd)...)
			input = append(input, c.AttachmentInputs(attachments)...)
			params := map[string]any{"threadId": serverID, "cwd": cwd, "input": input, "approvalPolicy": codexpolicy.ApprovalForPolicy(policy), "approvalsReviewer": codexpolicy.ReviewerForPolicy(policy), "sandboxPolicy": codexpolicy.TurnSandboxForPolicy(policy)}
			if modelID != "" {
				params["model"] = modelID
			}
			if effort != "" {
				params["effort"] = effort
			}
			if serviceTier != "" {
				params["serviceTierForTurn"] = serviceTier
			}
			mayHaveStarted = true
			err = client.Call(ctx, "turn/start", params, &result)
			if codex.IsMissingThread(err, serverID) {
				// The server explicitly rejected this turn before accepting input.
				// Restore the same thread and retry once, without another user bubble.
				missingThread = true
				mayHaveStarted = false
				err = client.Call(ctx, "thread/resume", map[string]any{"threadId": serverID, "cwd": cwd, "sandbox": codexpolicy.SandboxForPolicy(policy), "approvalPolicy": codexpolicy.ApprovalForPolicy(policy), "approvalsReviewer": codexpolicy.ReviewerForPolicy(policy)}, nil)
				if err == nil {
					input[0]["text"] = composer.CollaborationTaggedInput(mem.Text(text), req.Parts)
					mayHaveStarted = true
					err = client.Call(ctx, "turn/start", params, &result)
				}
			}
			mem.Finish(err == nil)
		}
		c.Dispatch(func() {
			if c.Snapshot().Client != client {
				c.ManagedCodexAccepted(req.ExecutionID, "", client.RuntimeEpoch(), errors.New("Codex connection changed before submission acknowledgement"), mayHaveStarted)
				return
			}
			c.ManagedCodexAccepted(req.ExecutionID, result.Turn.ID, client.RuntimeEpoch(), err, mayHaveStarted)
			if err != nil {
				c.Update(id, func(t *model.Thread) {
					t.Busy = false
					if missingThread {
						t.Resumed = false
					}
					t.Status = "Send failed: " + err.Error()
					c.Upsert(t, model.Message{Role: "error", Kind: "error", Status: "failed", Content: "Send failed: " + err.Error(), ItemID: "send-error-" + fmt.Sprint(c.NextID())})
					if req.ExecutionID == "" {
						t.Draft = text
					}
					t.Loaded = t.ServerID == ""
				})
				if req.ExecutionID == "" {
					c.ReturnDraft(id, text, attachments)
				}
				return
			}
			c.Update(id, func(t *model.Thread) {
				if t.Busy {
					t.TurnID = result.Turn.ID
					t.Status = "Working…"
				}
			})
			if t := c.ThreadWithID(id); t.StopRequested && t.Busy {
				b.Stop(t)
			}
		})
	}()
	return id
}
