package controller

import (
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	claude "miya/internal/claude"
	codex "miya/internal/codex"
	dg "miya/internal/delegation"
	subagent "miya/internal/subagent"
	textutil "miya/internal/textutil"
	os "os"
	filepath "path/filepath"
	strings "strings"
	time "time"
)

func (c *Controller) LoadClaudeHistory(t appmodel.Thread) {
	if t.HistoryLoaded || t.Loading {
		return
	}
	cwd := c.ThreadWorkspace(t.ID)
	c.Update(t.ID, func(t *appmodel.Thread) { t.Loading = true })
	done := make(chan struct{})
	c.ClaudeHistoryDone = done
	go func() {
		defer close(done)
		// Claude Code's own record is the truth, as the CLI may have continued
		// the session; the app's copy remains for sessions it cannot find.
		var stored appmodel.Thread
		var err error
		if path := appmodel.FindClaudeSession(cwd, t.ServerID); path != "" {
			stored.Messages, err = appmodel.ParseClaudeSession(path)
			stored.Resumed = true
		}
		if len(stored.Messages) == 0 {
			var data []byte
			data, err = os.ReadFile(appmodel.ClaudeHistoryPath(t.ServerID))
			if err == nil {
				err = json.Unmarshal(data, &stored)
			}
		}
		c.Dispatch(func() {
			if c.ThreadWorkspace(t.ID) != cwd || c.ThreadWithID(t.ID).ServerID != t.ServerID {
				return
			}
			c.Update(t.ID, func(t *appmodel.Thread) {
				t.Loading = false
				t.HistoryLoaded = true
				if err != nil {
					t.Status = "Local history is unavailable. Send a message to continue this conversation."
					return
				}
				t.Messages = stored.Messages
				for i := range t.Messages {
					t.Messages[i].ID = c.NextID()
				}
				t.Resumed = stored.Resumed
				t.Loaded = true
			})
			c.drainQueue(t.ID)
		})
	}()
}

func (c *Controller) sendClaude(req appmodel.SendRequest) int {
	text, paths, policy := req.Text, req.Attachments, req.Policy
	t := c.ThreadWithID(req.ThreadID)
	if t.ID != 0 && !t.HistoryLoaded {
		// A phone may have read history without opening it in the controller.
		// Hydrate that copy before publishing any new messages as live history.
		id := c.enqueue(t.ID, req)
		c.LoadClaudeHistory(t)
		return id
	}
	cwd := c.Cwd
	if t.ID != 0 {
		cwd = c.ThreadWorkspace(t.ID)
	} else {
		if req.Project != "" {
			cwd = req.Project
		}
		t = appmodel.Thread{ID: c.NextID(), Agent: appmodel.AgentClaude, PermissionPolicy: policy, ServerID: claude.NewSessionID(), Title: appmodel.ConversationTitle("", appmodel.ClaudePromptTitle(text), ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.AddThread(cwd, t)
	}
	id := t.ID
	if c.ClaudeRuns[id] == nil {
		t.MemoryEpoch++
		epoch := t.MemoryEpoch
		c.Update(id, func(t *appmodel.Thread) { t.MemoryEpoch = epoch })
	}
	pendingUserID := c.NextID()
	localTurn := c.Memories().BeginTurn(t, req, pendingUserID)
	now := time.Now().UnixMilli()
	c.Update(id, func(t *appmodel.Thread) {
		appmodel.ResetTurnOutput(t)
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
		shown, images := appmodel.AttachmentDisplay(text, paths)
		t.Messages = append(t.Messages, appmodel.Message{ID: pendingUserID, Role: "user", Content: shown, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, SentAtMs: now, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
		name, _, _ := strings.Cut(text, " ")
		if name == "/loop" || name == "/proactive" {
			appmodel.SetThreadTask(t, appmodel.ThreadTask{ID: "loop:pending", Kind: "loop", Title: strings.TrimSpace(strings.TrimPrefix(text, name)), Status: "starting"})
		}
	})
	if c.ClaudeRuns == nil {
		c.ClaudeRuns = map[int]*claude.Client{}
	}
	existing := c.ClaudeRuns[id]
	generation := c.ThreadWithID(id).ClaudeGeneration
	if existing == nil {
		generation++
		c.Update(id, func(t *appmodel.Thread) { t.ClaudeGeneration = generation })
	}
	var executionEpoch string
	if execution := c.Collaboration.Execution(req.ExecutionID); execution != nil {
		if existing != nil {
			_, _, _, execution.Epoch = existing.Runtime()
		} else {
			execution.Epoch = "claude:" + claude.NewSessionID()
		}
		executionEpoch = execution.Epoch
	}
	reject := func(err error) {
		c.Dispatch(func() {
			if c.ThreadWithID(id).ClaudeGeneration != generation {
				return
			}
			if existing == nil {
				c.FinishClaude(id, cwd, err)
				return
			}
			c.Update(id, func(t *appmodel.Thread) {
				t.Busy, t.TaskStopping = existing.Steerable(), false
				t.Status = err.Error()
				for i, m := range t.Messages {
					if m.ID == pendingUserID {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
				t.Messages = append(t.Messages, appmodel.Message{ID: c.NextID(), Role: "error", Content: err.Error()})
			})
			if req.ExecutionID == "" {
				c.ReturnDraft(id, text, paths)
			}
			state := dg.NeedsAttention
			if errors.Is(err, errManagedCancelled) {
				state = dg.Interrupted
			}
			if errors.Is(err, errManagedPaused) && strings.HasPrefix(req.ExecutionID, "delivery-") {
				s := c.Collaboration
				s.Post(func() {
					if e := s.Manager.DeliveryOutcome(req.ExecutionID, "", "not_started", err.Error()); e == nil {
						s.Release(req.ExecutionID)
					}
				})
			} else {
				c.ManagedEvent(req.ExecutionID, dg.Event{State: state, Result: dg.Result{Error: err.Error()}})
			}
		})
	}
	go func() {
		beforeInput := func(ctx context.Context) error {
			return c.persistManagedExecutionBinding(ctx, req.ExecutionID, appmodel.ThreadRef{Source: c.ReferenceSource, Backend: appmodel.AgentClaude, SessionID: t.ServerID}.URL(), executionEpoch)
		}
		mem := c.Memories().PrepareInput(t, req, cwd, c.ThreadRef(t).URL())
		content, err := appmodel.ClaudeInput(appmodel.CollaborationTaggedInput(mem.Text(text), req.Parts), paths)
		if err != nil {
			mem.Finish(false)
			reject(err)
			return
		}
		if existing != nil {
			ctx, cancel := context.WithTimeout(c.parentContext(), 10*time.Second)
			defer cancel()
			if req.KeepSessionSettings {
				err = beforeInput(ctx)
				if err == nil {
					err = existing.Send(t.ServerID, content)
				}
			} else {
				err = existing.SendWithOptions(ctx, claude.Options{SessionID: t.ServerID, Content: content, Model: req.Model, Effort: req.Effort, PermissionMode: appmodel.ClaudePermissionMode(policy), BeforeInput: beforeInput})
			}
			if err != nil {
				reject(err)
			}
			mem.Finish(err == nil)
			return
		}
		run, err := claude.Start(c.Ctx, claude.Options{Binary: c.ClaudeBinary, Args: append(append([]string(nil), c.ClaudeArgs...), appmodel.ClaudeMCPArgs(cwd)...), Env: append(append([]string(nil), c.ClaudeEnv...), appmodel.ClaudeMCPEnv()...), Cwd: cwd, SessionID: t.ServerID, Resume: t.Resumed, PermissionMode: appmodel.ClaudePermissionMode(policy), Model: req.Model, Effort: req.Effort, Content: content, BeforeInput: beforeInput}, func(f claude.Frame) {
			c.Dispatch(func() {
				if c.ThreadWorkspace(id) == cwd && c.ThreadWithID(id).ClaudeGeneration == generation {
					c.HandleClaude(id, f)
				}
			})
		}, func(err error) {
			c.Dispatch(func() {
				if c.ThreadWithID(id).ClaudeGeneration == generation {
					c.FinishClaude(id, cwd, err)
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
				c.FinishClaude(id, cwd, err)
				return
			}
			if c.ThreadWorkspace(id) != cwd {
				go run.Stop()
				return
			}
			c.ClaudeRuns[id] = run
			c.noteRuntime(c.ThreadWithID(id))
			if current := c.ThreadWithID(id); current.ID != 0 && current.StopRequested {
				go run.Stop()
			}
		})
	}()
	return id
}

func (c *Controller) FinishClaude(id int, cwd string, err error) {
	defer func() {
		t := c.ThreadWithID(id)
		state := dg.Interrupted
		if !errors.Is(err, errManagedCancelled) && !t.StopRequested && (err != nil || t.Status != "") {
			state = dg.Failed
		}
		if errors.Is(err, errManagedPaused) && strings.HasPrefix(t.ExecutionID, "delivery-") {
			s := c.Collaboration
			s.Post(func() {
				if e := s.Manager.DeliveryOutcome(t.ExecutionID, "", "not_started", err.Error()); e == nil {
					s.Release(t.ExecutionID)
				}
			})
			return
		}
		c.managedClaudeResult(id, state, "")
	}()
	// The run's conversation may be in a background project.
	here := c.ThreadWorkspace(id) == cwd
	if here {
		sid := c.ThreadWithID(id).ServerID
		go func() {
			path := appmodel.FindClaudeSession(cwd, sid)
			if path == "" {
				return
			}
			session, ok := appmodel.SummarizeClaudeSession(path)
			if !ok {
				return
			}
			c.Dispatch(func() {
				if c.ThreadWorkspace(id) != cwd {
					return
				}
				c.Update(id, func(t *appmodel.Thread) {
					if t.ServerID == sid {
						t.Title = session.Title
					}
				})
			})
		}()
	}
	delete(c.ClaudeRuns, id)
	if !here {
		return
	}
	c.Update(id, func(t *appmodel.Thread) {
		t.Busy = false
		t.TaskStopping = false
		t.CompletedAtMs = time.Now().UnixMilli()
		// A result frame can report an API error even when the CLI exits
		// successfully. Keep that turn's error after the process is reaped.
		resultError := ""
		for i := len(t.Messages) - 1; i >= 0; i-- {
			if t.Messages[i].Role == "user" {
				break
			}
			if t.Messages[i].Role == "error" {
				resultError = t.Messages[i].Content
				break
			}
		}
		state := "interrupted"
		if t.StopRequested {
			state = "stopped"
		} else if err != nil || resultError != "" {
			state = "failed"
		}
		appmodel.StopClaudeTasks(t, state)
		agentState := subagent.Interrupted
		if state == "failed" {
			agentState = subagent.Failed
		}
		appmodel.InterruptClaudeAgents(t, agentState)
		t.Status = ""
		if t.StopRequested {
			t.Status = "Stopped"
		} else if err != nil {
			t.Status = err.Error()
			t.Messages = append(t.Messages, appmodel.Message{ID: c.NextID(), Role: "error", Content: err.Error(), StartedAtMs: t.CompletedAtMs})
		} else {
			t.Status = resultError
		}
		for i := range t.Messages {
			if t.Messages[i].Status == "inProgress" {
				t.Messages[i].Status = "completed"
				if t.StopRequested {
					t.Messages[i].Status = "interrupted"
				} else if err != nil || resultError != "" {
					t.Messages[i].Status = "failed"
				}
			}
		}
		appmodel.CompleteClaudeTiming(t)
		appmodel.SaveClaudeHistory(*t)
	})
	pending := []codex.Approval{}
	prefix := fmt.Sprintf("\"claude:%d:", id)
	for _, a := range c.Approvals() {
		if !strings.HasPrefix(string(a.ID), prefix) {
			pending = append(pending, a)
		}
	}
	c.SetApprovals(pending)
}

func (c *Controller) HandleClaude(id int, f claude.Frame) {
	if f.ParentToolUseID == "" && (f.Type == "assistant" || f.Type == "stream_event" || f.Type == "control_request") {
		c.managedClaudeStarted(id)
	}
	if f.Type == "assistant" || f.Type == "user" || f.Type == "system" || f.Type == "result" {
		c.noteRuntime(c.ThreadWithID(id))
	}
	if f.Type == "system" && f.Subtype == "init" {
		c.NoteClaudeModel(f.Model)
		if f.Model != "" {
			c.Update(id, func(t *appmodel.Thread) { t.LaunchModel = f.Model })
		}
	}
	if f.Type == "control_cancel_request" {
		rid, _ := json.Marshal(fmt.Sprintf("claude:%d:%s", id, f.RequestID))
		c.removeRequest(rid)
		return
	}

	if f.Type == "control_request" {
		rid, _ := json.Marshal(fmt.Sprintf("claude:%d:%s", id, f.RequestID))
		if f.Request.Subtype != "can_use_tool" {
			if run := c.ClaudeRuns[id]; run != nil {
				go run.Reply(f.RequestID, map[string]any{"behavior": "deny", "message": "Unsupported request"})
			}
			return
		}
		// Phones route request cards by the session ID, including requests
		// from conversations in background projects.
		a := codex.Approval{ID: rid, ThreadID: c.ThreadWithID(id).ServerID, ItemID: f.Request.ToolUseID, Method: "claude/permission", Command: f.Request.ToolName, Reason: string(f.Request.Input), Payload: f.Request.Input}
		if f.Request.ToolName == "AskUserQuestion" {
			var input struct {
				Questions []struct {
					Question, Header string
					Options          []struct{ Label, Description string }
				}
			}
			_ = json.Unmarshal(f.Request.Input, &input)
			a.Method = "item/tool/requestUserInput"
			for _, q := range input.Questions {
				question := codex.Question{ID: q.Question, Header: q.Header, Question: q.Question}
				for _, o := range q.Options {
					question.Options = append(question.Options, struct {
						Label       string `json:"label"`
						Description string `json:"description"`
					}{Label: o.Label, Description: o.Description})
				}
				a.Questions = append(a.Questions, question)
			}
		}
		c.SetApprovals(append(append([]codex.Approval(nil), c.Approvals()...), a))
		return
	}
	// Set only by a successful top-level result. The thread's Status is display
	// text that stays "running" until the process exits for ordinary turns, so
	// it cannot decide whether this turn completed.
	memoryTurnDone := false
	c.Update(id, func(t *appmodel.Thread) {
		defer func() { appmodel.ApplyClaudeAgentTasks(t, f.AgentTasks) }()
		now := time.Now().UnixMilli()
		t.LastOutputAtMs = now
		if f.SchedulesKnown {
			appmodel.ApplyClaudeSchedules(t, f.Schedules)
		}
		if f.Type == "command_lifecycle" && f.CommandState == "started" {
			if !t.Busy {
				appmodel.ResetTurnOutput(t)
				t.StartedAtMs = now
			}
			t.Busy, t.StopRequested = true, false
			t.CompletedAtMs = 0
			t.Status = "Claude Code is running…"
		}
		if f.SessionID != "" {
			t.ServerID = f.SessionID
			t.Resumed = true
		}
		if f.Type == "system" && strings.HasPrefix(f.Subtype, "task_") {
			appmodel.ClaudeTaskFrame(t, f)
			return
		}
		// A sub-agent's own frames are steps of its Task call, not messages
		// of the conversation.
		if f.ParentToolUseID != "" {
			if f.Type == "assistant" {
				appmodel.ClaudeSubAgentStep(t, f.ParentToolUseID, f.Message.Content)
			}
			return
		}
		switch f.Type {
		case "stream_event":
			var e struct {
				Type    string
				Index   int
				Message claude.Message
				Delta   struct{ Type, Text string }
				Usage   json.RawMessage
			}
			if json.Unmarshal(f.Event, &e) != nil {
				return
			}
			if e.Type == "message_start" {
				t.ClaudeMessageID = e.Message.ID
				appmodel.RecordClaudeOutput(t, e.Message.ID, e.Message.Usage)
			}
			if e.Type == "message_delta" {
				appmodel.RecordClaudeOutput(t, t.ClaudeMessageID, e.Usage)
			}
			if e.Type == "content_block_delta" && e.Delta.Type == "text_delta" {
				key := fmt.Sprintf("%s:%d", t.ClaudeMessageID, e.Index)
				for i := range t.Messages {
					if t.Messages[i].ItemID == key {
						t.Messages[i].Content += e.Delta.Text
						return
					}
				}
				t.Messages = append(t.Messages, appmodel.Message{ID: c.NextID(), Role: "assistant", Kind: "agentMessage", ItemID: key, Content: e.Delta.Text, SentAtMs: now, StartedAtMs: now})
			}
		case "assistant":
			appmodel.RecordClaudeOutput(t, f.Message.ID, f.Message.Usage)
			for i, b := range f.Message.Content {
				if b.Type == "text" {
					key := appmodel.ClaudeTextKey(t, f.Message.ID, i, b.Text)
					c.upsert(t, appmodel.Message{Role: "assistant", Kind: "agentMessage", ItemID: key, Content: b.Text, SentAtMs: now, StartedAtMs: now})
				}
				if b.Type == "tool_use" {
					m := appmodel.Message{Role: "tool", Kind: "claudeTool", ItemID: b.ID, Content: b.Name + "\n" + string(b.Input), Status: "inProgress", Agents: appmodel.ClaudeSubAgent(b.ID, b.Name, b.Input, "inProgress")}
					subagent.Keep(t, &m)
					c.upsert(t, m)
				}
			}
		case "user":
			for _, b := range f.Message.Content {
				if b.Type == "tool_result" {
					for i := range t.Messages {
						if t.Messages[i].ItemID == b.ToolUseID {
							t.Messages[i].Status = "completed"
							if b.IsError {
								t.Messages[i].Status = "failed"
							}
							output := appmodel.ClaudeVisibleText(appmodel.ClaudeBlocks(b.Content))
							if shown := appmodel.ClaudeToolOutput(t.Messages[i], output); shown != "" {
								t.Messages[i].Content += "\n" + textutil.Bounded(shown)
							}
							appmodel.ClaudeFinishSubAgents(&t.Messages[i], output, f.ToolUseResult)
						}
					}
				}
			}
		case "result":
			t.TaskStopping = false
			// Interrupting Claude Code can itself produce an error result (for
			// example an ede_diagnostic with stop_reason=tool_use).
			// finishClaude will finalize it as a requested stop.
			if t.StopRequested {
				return
			}
			hadOutput := appmodel.TurnHasOutput(t.Messages)
			emptyResult := !f.IsError && !f.KeepingAlive && !hadOutput && strings.TrimSpace(f.Result) == ""
			if f.IsError || emptyResult {
				appmodel.StopClaudeTasks(t, "failed")
			}
			if f.KeepingAlive {
				t.Busy = f.WaitingForAgents
				if f.WaitingForAgents {
					t.CompletedAtMs = 0
					t.Status = "Waiting for subagents to finish…"
				} else {
					t.CompletedAtMs = now
					t.Status = ""
				}
			}
			if f.IsError || emptyResult {
				message := strings.Join(f.Errors, "\n")
				if message == "" {
					message = f.Result
				}
				if message == "" {
					message = "Claude Code run failed"
				}
				if emptyResult {
					message = "Claude Code finished the turn without returning a response."
				}
				message = appmodel.ClaudeErrorMessage(message, hadOutput, c.ClaudeCommand != "")
				t.Status = message
				t.Messages = append(t.Messages, appmodel.Message{ID: c.NextID(), Role: "error", Content: message, StartedAtMs: now})
			} else if !hadOutput && strings.TrimSpace(f.Result) != "" {
				// Some CLI transports only provide the final result text.
				t.Messages = append(t.Messages, appmodel.Message{ID: c.NextID(), Role: "assistant", Kind: "agentMessage", Content: f.Result, SentAtMs: now, StartedAtMs: now})
			}
			if f.KeepingAlive && !f.WaitingForAgents {
				appmodel.CompleteClaudeTiming(t)
				appmodel.SaveClaudeHistory(*t)
			}
			memoryTurnDone = !f.IsError && !emptyResult && !f.WaitingForAgents
		}
	})
	if memoryTurnDone {
		c.Memories().CompletedTurn(id, f.UUID)
	}
	if f.Type == "result" && f.ParentToolUseID == "" && !f.WaitingForAgents {
		state := dg.Completed
		if c.ThreadWithID(id).StopRequested {
			state = dg.Interrupted
		} else if f.IsError {
			state = dg.Failed
		}
		c.managedClaudeResult(id, state, f.UUID)
	}
}

func (c *Controller) RespondClaude(a codex.Approval, result any) bool {
	var id string
	_ = json.Unmarshal(a.ID, &id)
	if !strings.HasPrefix(id, "claude:") {
		return false
	}
	var thread int
	_, _ = fmt.Sscanf(id, "claude:%d:", &thread)
	parts := strings.SplitN(id, ":", 3)
	c.removeRequest(a.ID)
	run := c.ClaudeRuns[thread]
	if run == nil || len(parts) != 3 {
		return true
	}
	encoded, _ := json.Marshal(result)
	var r struct {
		Decision string `json:"decision"`
		Answers  map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	_ = json.Unmarshal(encoded, &r)
	response := map[string]any{"behavior": "deny", "message": "The user denied this operation"}
	if r.Decision == "accept" || r.Answers != nil {
		var input map[string]any
		_ = json.Unmarshal(a.Payload, &input)
		if input == nil {
			input = map[string]any{}
		}
		if r.Answers != nil {
			answers := map[string]string{}
			for k, v := range r.Answers {
				answers[k] = strings.Join(v.Answers, ", ")
			}
			input["answers"] = answers
		}
		response = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	go func() { _ = run.Reply(parts[2], response) }()
	return true
}
