package codex

import (
	"context"
	"time"

	codex "miya/internal/agent/codex/native"
	codexpolicy "miya/internal/agent/codex/policy"
	codexprojection "miya/internal/agent/codex/projection"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
)

func (b Adapter) Archive(t model.Thread) {
	c := b.host
	client := c.Snapshot().Client
	if client == nil || t.ServerID == "" {
		c.RemoveThread(t.ID, t.ServerID)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
		defer cancel()
		err := client.Call(ctx, "thread/archive", map[string]string{"threadId": t.ServerID}, nil)
		c.Dispatch(func() {
			if err != nil {
				c.SetStatus("Unable to archive conversation: " + err.Error())
				return
			}
			c.RemoveThread(t.ID, t.ServerID)
		})
	}()
}

// deleteThread removes the local entry immediately. Codex currently exposes
// archive as the server-side removal operation, so synchronize that state when
// a server-backed thread is deleted as well.
func (b Adapter) Delete(t model.Thread) {
	c := b.host
	c.RemoveThread(t.ID, t.ServerID)
	if c.Snapshot().Client == nil || t.ServerID == "" {
		return
	}
	client := c.Snapshot().Client
	go func() {
		ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
		defer cancel()
		if err := client.Call(ctx, "thread/archive", map[string]string{"threadId": t.ServerID}, nil); err != nil {
			c.Dispatch(func() { c.SetStatus("Unable to delete conversation: " + err.Error()) })
		}
	}()
}
func (b Adapter) Resume(t model.Thread, after *model.SendRequest) {
	c := b.host
	client := c.Snapshot().Client
	cwd := c.ThreadWorkspace(t.ID)
	policy := c.PermissionForThread(t)
	if client == nil {
		c.Update(t.ID, func(t *model.Thread) {
			t.Busy = false
			t.Loading = false
			t.Status = "Codex is reconnecting…"
		})
		if after != nil {
			if after.ExecutionID == "" {
				c.ReturnDraft(t.ID, after.Text, after.Attachments)
			} else {
				c.BeforeInputFailed(after.ExecutionID, "Codex is not connected before resuming")
			}
		}
		return
	}
	c.Update(t.ID, func(t *model.Thread) {
		t.Busy = true
		t.Loading = true
		t.Status = "Loading conversation…"
	})
	go func() {
		ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
		defer cancel()
		var result codex.ThreadResponse
		err := client.Call(ctx, "thread/resume", map[string]any{"threadId": t.ServerID, "cwd": cwd, "sandbox": codexpolicy.SandboxForPolicy(policy), "approvalPolicy": codexpolicy.ApprovalForPolicy(policy), "approvalsReviewer": codexpolicy.ReviewerForPolicy(policy)}, &result)
		readOnly := codex.IsWriterConflict(err)
		if readOnly {
			// Another Codex process owns the writer. Read the persisted rollout through
			// thread/read so this client can still act as a conversation viewer.
			var readResult codex.ThreadResponse
			readErr := client.Call(ctx, "thread/read", map[string]any{"threadId": t.ServerID, "includeTurns": true}, &readResult)
			if readErr == nil {
				result = readResult
				err = nil
			}
		}
		if err == nil && !readOnly {
			err = codexpolicy.Verify(policy, result.Sandbox, result.ApprovalPolicy)
		}
		// Decode attachments and normalize the complete rollout off the UI thread.
		// Runtime IDs are assigned only after returning to the UI owner.
		var messages []model.Message
		if err == nil {
			messages = codexprojection.HistoryMessages(result.Thread)
		}

		c.Dispatch(func() {
			if c.Snapshot().Client != client {
				c.Update(t.ID, func(t *model.Thread) {
					t.Loading = false
					t.Busy = false
					t.Status = "Connection changed while loading. Click to retry."
				})
				if after != nil {
					if after.ExecutionID == "" {
						c.ReturnDraft(t.ID, after.Text, after.Attachments)
					} else {
						c.BeforeInputFailed(after.ExecutionID, "Codex connection changed before native input was submitted")
					}
				}
				return
			}
			c.Update(t.ID, func(t *model.Thread) {
				t.Busy = false
				t.Loading = false
				t.ReadOnly = readOnly
				t.Resumed = err == nil && !readOnly
				if err != nil {
					t.Status = err.Error()
					c.Upsert(t, model.Message{Role: "error", Kind: "error", Status: "failed", Content: "Unable to load conversation: " + err.Error(), ItemID: "resume-error"})
					return
				}
				t.Loaded = true
				t.Resumed = !readOnly
				t.NativeSandbox = result.Sandbox
				t.HistoryLoaded = true
				t.ReadOnly = readOnly
				if readOnly {
					t.Status = "Read-only · This conversation is running in another window"
				} else {
					t.Status = ""
				}
				t.Title = msgfmt.ConversationTitle(result.Thread.Name, result.Thread.Preview, t.Title)
				for i := range messages {
					messages[i].ID = c.NextID()
				}
				t.Messages = messages
				for _, turn := range result.Thread.Turns {
					if turn.Status == "inProgress" && !readOnly {
						t.Busy = true
						t.TurnID = turn.ID
						t.StartedAtMs, t.CompletedAtMs = codexprojection.CodexTurnTimes(turn)
						t.Status = "Working…"
					}
				}
			})
			if err == nil {
				c.RefreshGoal(t.ID)
			}
			if after != nil && (err != nil || readOnly || c.Submit(*after) == 0) {
				if after.ExecutionID != "" {
					reason := "Unable to resume target session before submission"
					if err != nil {
						reason = err.Error()
					}
					c.BeforeInputFailed(after.ExecutionID, reason)
				}
				if after.ExecutionID == "" {
					c.ReturnDraft(t.ID, after.Text, after.Attachments)
				}
			}
		})
	}()
}
func (b Adapter) Interrupt(t model.Thread) {
	c := b.host
	client := c.Snapshot().Client
	if client == nil {
		c.Update(t.ID, func(t *model.Thread) { t.StopRequested = false; t.Status = "Codex is reconnecting…" })
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
		defer cancel()
		err := client.Call(ctx, "turn/interrupt", map[string]string{"threadId": t.ServerID, "turnId": t.TurnID}, nil)
		if err != nil {
			c.Dispatch(func() { c.Update(t.ID, func(t *model.Thread) { t.Status = "Stop failed: " + err.Error() }) })
		}
	}()
}
