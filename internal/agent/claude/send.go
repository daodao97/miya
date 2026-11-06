package claude

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	agentmeta "miya/internal/agent/catalog"
	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	"miya/internal/composer"
	dg "miya/internal/delegation"
	mcpconfig "miya/internal/mcp/config"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

func (b Adapter) Send(req model.SendRequest) int {
	c := b.host
	text, paths, policy := req.Text, req.Attachments, req.Policy
	t := c.ThreadWithID(req.ThreadID)
	if t.ID != 0 && !t.HistoryLoaded {
		// A phone may have read history without opening it in the controller.
		// Hydrate that copy before publishing any new messages as live history.
		id := c.Enqueue(t.ID, req)
		b.LoadHistory(t)
		return id
	}
	cwd := c.Cwd()
	if t.ID != 0 {
		cwd = c.ThreadWorkspace(t.ID)
	} else {
		if req.Project != "" {
			cwd = req.Project
		}
		t = model.Thread{ID: c.NextID(), Agent: agentmeta.Claude, PermissionPolicy: policy, ServerID: claude.NewSessionID(), Title: msgfmt.ConversationTitle("", transcript.PromptTitle(text), ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.AddThread(cwd, t)
	}
	id := t.ID
	if c.Run(id) == nil {
		t.MemoryEpoch++
		epoch := t.MemoryEpoch
		c.Update(id, func(t *model.Thread) { t.MemoryEpoch = epoch })
	}
	pendingUserID := c.NextID()
	localTurn := c.Memories().BeginTurn(t, req, pendingUserID)
	now := time.Now().UnixMilli()
	c.Update(id, func(t *model.Thread) {
		msgfmt.ResetTurnOutput(t)
		t.ExecutionID = req.ExecutionID
		if !req.KeepSessionSettings {
			t.LaunchModel, t.LaunchEffort = req.Model, req.Effort
		}
		t.Busy = true
		t.StopRequested = false
		t.StartedAtMs = now
		t.CompletedAtMs = 0
		t.LastOutputAtMs = now
		t.LastActivityAt = now
		t.Status = "Claude Code is running…"
		shown, images := msgfmt.AttachmentDisplay(text, paths)
		t.Messages = append(t.Messages, model.Message{ID: pendingUserID, Role: "user", Content: shown, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, SentAtMs: now, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
		name, _, _ := strings.Cut(text, " ")
		if name == "/loop" || name == "/proactive" {
			model.SetThreadTask(t, model.ThreadTask{ID: "loop:pending", Kind: "loop", Title: strings.TrimSpace(strings.TrimPrefix(text, name)), Status: "starting"})
		}
	})
	existing := c.Run(id)
	generation := c.ThreadWithID(id).ClaudeGeneration
	if existing == nil {
		generation++
		c.Update(id, func(t *model.Thread) { t.ClaudeGeneration = generation })
	}
	proposedEpoch := "claude:" + claude.NewSessionID()
	if existing != nil {
		_, _, _, proposedEpoch = existing.Runtime()
	}
	executionEpoch := c.ReserveExecutionEpoch(req.ExecutionID, proposedEpoch)
	reject := func(err error) {
		c.Dispatch(func() {
			if c.ThreadWithID(id).ClaudeGeneration != generation {
				return
			}
			if existing == nil {
				b.Finish(id, cwd, err)
				return
			}
			c.Update(id, func(t *model.Thread) {
				t.Busy, t.TaskStopping = existing.Steerable(), false
				t.Status = err.Error()
				for i, m := range t.Messages {
					if m.ID == pendingUserID {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
				t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: err.Error()})
			})
			if req.ExecutionID == "" {
				c.ReturnDraft(id, text, paths)
			}
			state := dg.NeedsAttention
			if c.Cancelled(err) {
				state = dg.Interrupted
			}
			if !c.FinishPausedDelivery(req.ExecutionID, err) {
				c.ManagedEvent(req.ExecutionID, dg.Event{State: state, Result: dg.Result{Error: err.Error()}})
			}
		})
	}
	go func() {
		beforeInput := func(ctx context.Context) error {
			return c.BindExecution(ctx, req.ExecutionID, c.ThreadRef(model.Thread{Agent: agentmeta.Claude, ServerID: t.ServerID}).URL(), executionEpoch)
		}
		mem := c.Memories().PrepareInput(t, req, cwd, c.ThreadRef(t).URL())
		content, err := claude.Input(composer.CollaborationTaggedInput(mem.Text(text), req.Parts), paths)
		if err != nil {
			mem.Finish(false)
			reject(err)
			return
		}
		if existing != nil {
			ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
			defer cancel()
			if req.KeepSessionSettings {
				err = beforeInput(ctx)
				if err == nil {
					err = existing.Send(t.ServerID, content)
				}
			} else {
				err = existing.SendWithOptions(ctx, claude.Options{SessionID: t.ServerID, Content: content, Model: req.Model, Effort: req.Effort, PermissionMode: claude.PermissionMode(policy), BeforeInput: beforeInput})
			}
			if err != nil {
				reject(err)
			}
			mem.Finish(err == nil)
			return
		}
		run, err := claude.Start(c.Context(), claude.Options{Binary: c.Snapshot().Binary, Args: append(append([]string(nil), c.Snapshot().Args...), mcpconfig.ClaudeArgs(cwd)...), Env: append(append([]string(nil), c.Snapshot().Env...), mcpconfig.ClaudeEnv()...), Cwd: cwd, SessionID: t.ServerID, Resume: t.Resumed, PermissionMode: claude.PermissionMode(policy), Model: req.Model, Effort: req.Effort, Content: content, BeforeInput: beforeInput}, func(f claude.Frame) {
			c.Dispatch(func() {
				if c.ThreadWorkspace(id) == cwd && c.ThreadWithID(id).ClaudeGeneration == generation {
					b.Handle(id, f)
				}
			})
		}, func(err error) {
			c.Dispatch(func() {
				if c.ThreadWithID(id).ClaudeGeneration == generation {
					b.Finish(id, cwd, err)
				}
			})
		})
		mem.Finish(err == nil)
		c.Dispatch(func() {
			if c.ThreadWithID(id).ClaudeGeneration != generation {
				if run != nil {
					go run.Stop()
				}
				return
			}
			if err != nil {
				b.Finish(id, cwd, err)
				return
			}
			if c.ThreadWorkspace(id) != cwd {
				go run.Stop()
				return
			}
			c.SetRun(id, run)
			c.NoteRuntime(c.ThreadWithID(id))
			if current := c.ThreadWithID(id); current.ID != 0 && current.StopRequested {
				go run.Stop()
			}
		})
	}()
	return id
}
