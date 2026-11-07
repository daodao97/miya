package pi

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	agentmeta "miya/internal/agent/catalog"
	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	chattext "miya/internal/chat/text"
	dg "miya/internal/delegation"
)

func (b Adapter) Send(req model.SendRequest) int {
	c := b.host
	t := c.ThreadWithID(req.ThreadID)
	if t.ID != 0 && !t.HistoryLoaded {
		id := c.Enqueue(t.ID, req)
		b.LoadHistory(t)
		return id
	}
	snap := c.Snapshot()
	if snap.Binary == "" {
		missing := firstOf(snap.Error, tr("pi not found. Install it with npm install -g @mariozechner/pi-coding-agent."))
		c.SetStatus(missing)
		if req.ExecutionID == "" {
			if t.ID != 0 {
				c.ReturnDraft(t.ID, req.Text, req.Attachments)
			}
		} else if !c.FinishPausedDelivery(req.ExecutionID, errors.New(missing)) {
			c.ManagedEvent(req.ExecutionID, dg.Event{State: dg.NeedsAttention, Result: dg.Result{Error: missing}})
		}
		return 0
	}
	cwd := c.Cwd()
	if t.ID != 0 {
		cwd = c.ThreadWorkspace(t.ID)
	} else {
		if req.Project != "" {
			cwd = req.Project
		}
		t = model.Thread{ID: c.NextID(), Agent: agentmeta.Pi, PermissionPolicy: req.Policy, Title: msgfmt.ConversationTitle("", chattext.PromptTitle(req.Text), ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.AddThread(cwd, t)
	}
	id := t.ID
	existing := c.Run(id)
	steering := existing != nil && existing.Streaming()
	if existing == nil {
		t.MemoryEpoch++
		epoch := t.MemoryEpoch
		c.Update(id, func(t *model.Thread) { t.MemoryEpoch = epoch })
	}
	pendingUserID := c.NextID()
	localTurn := c.Memories().BeginTurn(t, req, pendingUserID)
	now := time.Now().UnixMilli()
	c.Update(id, func(t *model.Thread) {
		if !steering {
			msgfmt.ResetTurnOutput(t)
			t.ExecutionID = req.ExecutionID
			if !req.KeepSessionSettings {
				t.LaunchModel, t.LaunchEffort = req.Model, req.Effort
			}
			t.StartedAtMs, t.CompletedAtMs = now, 0
			t.StopRequested = false
		}
		t.Busy = true
		t.LastOutputAtMs, t.LastActivityAt = now, now
		t.Status = tr("Pi is running…")
		shown, images := msgfmt.AttachmentDisplay(req.Text, req.Attachments)
		t.Messages = append(t.Messages, model.Message{ID: pendingUserID, Role: "user", Content: shown, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, SentAtMs: now, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
	})
	proposedEpoch := "pi:new"
	if existing != nil {
		proposedEpoch = existing.Epoch()
	}
	executionEpoch := c.ReserveExecutionEpoch(req.ExecutionID, proposedEpoch)
	reject := func(err error) {
		c.Dispatch(func() {
			run := c.Run(id)
			c.Update(id, func(t *model.Thread) {
				for i, m := range t.Messages {
					if m.ID == pendingUserID {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
				t.Busy = run != nil && run.Streaming()
				t.Status = err.Error()
				t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: err.Error()})
			})
			if req.ExecutionID == "" {
				c.ReturnDraft(id, req.Text, req.Attachments)
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
	modelID := firstOf(req.Model, defaultModel(snap))
	thinking := req.Effort
	go func() {
		mem := c.Memories().PrepareInput(t, req, cwd, c.ThreadRef(t).URL())
		text, images, err := pinative.Input(composer.CollaborationTaggedInput(mem.Text(req.Text), req.Parts), req.Attachments)
		if err != nil {
			mem.Finish(false)
			reject(err)
			return
		}
		ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
		defer cancel()
		run := existing
		serverID := t.ServerID
		if run == nil {
			run, serverID, err = b.start(ctx, id, cwd, t, modelID, thinking)
			if err != nil {
				mem.Finish(false)
				reject(err)
				return
			}
		} else if !steering && !req.KeepSessionSettings {
			// The process keeps the previous turn's settings until told.
			if modelID != "" && snap.Provider.Usable() {
				err = run.SetModel(ctx, pinative.ProviderName, modelID)
			}
			if err == nil && thinking != "" {
				err = run.SetThinking(ctx, thinking)
			}
		}
		if err == nil && req.ExecutionID != "" {
			if executionEpoch == "pi:new" {
				executionEpoch = c.ReserveExecutionEpoch(req.ExecutionID, run.Epoch())
			}
			err = c.BindExecution(ctx, req.ExecutionID, c.ThreadRef(model.Thread{Agent: agentmeta.Pi, ServerID: serverID}).URL(), executionEpoch)
		}
		if err == nil {
			err = run.Prompt(ctx, text, images)
		}
		mem.Finish(err == nil)
		if err != nil {
			reject(err)
		}
	}()
	return id
}

// start launches pi for a conversation and learns its session before the
// first prompt, so the conversation can be addressed while the turn runs.
func (b Adapter) start(ctx context.Context, id int, cwd string, t model.Thread, modelID, thinking string) (*pinative.Client, string, error) {
	c := b.host
	snap := c.Snapshot()
	opts := pinative.Options{Binary: snap.Binary, Args: snap.Args, Env: snap.Env, Cwd: cwd, Provider: snap.Provider, Models: providerModels(snap), Thinking: thinking}
	if snap.Provider.Usable() {
		ext, err := pinative.EnsureExtension()
		if err != nil {
			return nil, "", err
		}
		opts.Extension, opts.Model = ext, modelID
	} else {
		opts.Model = modelID
	}
	if t.ServerID != "" {
		if rec, err := pinative.LoadRecord(t.ServerID); err == nil {
			opts.SessionFile = rec.SessionFile
		}
	}
	var run *pinative.Client
	ready := make(chan struct{})
	run, err := pinative.Start(c.Context(), opts, func(e pinative.Event) {
		<-ready
		c.Dispatch(func() {
			if c.Run(id) == run && c.ThreadWorkspace(id) == cwd {
				b.Handle(id, e)
			}
		})
	}, func(err error) {
		<-ready
		c.Dispatch(func() {
			if c.Run(id) == run {
				b.Finish(id, run, err)
			}
		})
	})
	if err != nil {
		return nil, "", err
	}
	state, err := run.State(ctx)
	if err != nil {
		close(ready)
		go run.Stop()
		select {
		case <-run.Done():
		case <-time.After(time.Second):
		}
		return nil, "", err
	}
	registered := make(chan bool, 1)
	c.Dispatch(func() {
		if c.ThreadWithID(id).ID == 0 || c.ThreadWorkspace(id) != cwd || c.ThreadWithID(id).StopRequested {
			registered <- false
			return
		}
		c.SetRun(id, run)
		c.Update(id, func(t *model.Thread) {
			t.ServerID, t.Resumed = state.SessionID, true
			if state.Model != nil {
				t.LaunchModel = state.Model.ID
			}
			pinative.SaveRecord(*t, state.SessionFile)
		})
		c.NoteRuntime(c.ThreadWithID(id))
		registered <- true
	})
	close(ready)
	if !<-registered {
		go run.Stop()
		return nil, "", context.Canceled
	}
	return run, state.SessionID, nil
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
