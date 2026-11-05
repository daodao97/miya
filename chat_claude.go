package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/appdir"
	"miya/internal/claude"
	"miya/internal/codex"
	dg "miya/internal/delegation"
	"miya/internal/subagent"
	"miya/internal/textutil"
)

func claudeHistoryPath(id string) string {
	if id == "" || filepath.Base(id) != id {
		return ""
	}
	dir := appdir.File("quickgui-claude")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id+".json")
}
func saveClaudeHistory(t Thread) {
	p := claudeHistoryPath(t.ServerID)
	if p == "" {
		return
	}
	data, err := json.Marshal(t)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".history-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(name, p)
	}
}
func (c *chatController) loadClaudeHistory(t Thread) {
	if t.HistoryLoaded || t.Loading {
		return
	}
	cwd := c.threadWorkspace(t.ID)
	c.update(t.ID, func(t *Thread) { t.Loading = true })
	done := make(chan struct{})
	c.claudeHistoryDone = done
	go func() {
		defer close(done)
		// Claude Code's own record is the truth, as the CLI may have continued
		// the session; the app's copy remains for sessions it cannot find.
		var stored Thread
		var err error
		if path := findClaudeSession(cwd, t.ServerID); path != "" {
			stored.Messages, err = parseClaudeSession(path)
			stored.Resumed = true
		}
		if len(stored.Messages) == 0 {
			var data []byte
			data, err = os.ReadFile(claudeHistoryPath(t.ServerID))
			if err == nil {
				err = json.Unmarshal(data, &stored)
			}
		}
		c.dispatch(func() {
			if c.threadWorkspace(t.ID) != cwd || c.threadWithID(t.ID).ServerID != t.ServerID {
				return
			}
			c.update(t.ID, func(t *Thread) {
				t.Loading = false
				t.HistoryLoaded = true
				if err != nil {
					t.Status = "Local history is unavailable. Send a message to continue this conversation."
					return
				}
				t.Messages = stored.Messages
				for i := range t.Messages {
					t.Messages[i].ID = c.nextID()
				}
				t.Resumed = stored.Resumed
				t.Loaded = true
			})
			c.drainQueue(t.ID)
		})
	}()
}

func claudeInput(text string, paths []string) ([]map[string]any, error) {
	blocks := []map[string]any{{"type": "text", "text": text}}
	for _, p := range paths {
		typ := mime.TypeByExtension(filepath.Ext(p))
		if strings.HasPrefix(typ, "image/") {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if len(data) > 20*1024*1024 {
				return nil, fmt.Errorf("Image is too large: %s", filepath.Base(p))
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": typ, "data": base64.StdEncoding.EncodeToString(data)}})
		} else {
			blocks = append(blocks, map[string]any{"type": "text", "text": "Attached file: " + p})
		}
	}
	return blocks, nil
}
func (c *chatController) sendClaude(req SendRequest) int {
	text, paths, policy := req.Text, req.Attachments, req.Policy
	t := c.threadWithID(req.ThreadID)
	if t.ID != 0 && !t.HistoryLoaded {
		// A phone may have read history without opening it in the controller.
		// Hydrate that copy before publishing any new messages as live history.
		id := c.enqueue(t.ID, req)
		c.loadClaudeHistory(t)
		return id
	}
	cwd := c.cwd
	if t.ID != 0 {
		cwd = c.threadWorkspace(t.ID)
	} else {
		if req.Project != "" {
			cwd = req.Project
		}
		t = Thread{ID: c.nextID(), Agent: agentClaude, PermissionPolicy: policy, ServerID: claude.NewSessionID(), Title: conversationTitle("", claudePromptTitle(text), ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.addThread(cwd, t)
	}
	id := t.ID
	if c.claudeRuns[id] == nil {
		t.MemoryEpoch++
		epoch := t.MemoryEpoch
		c.update(id, func(t *Thread) { t.MemoryEpoch = epoch })
	}
	pendingUserID := c.nextID()
	localTurn := c.memories().BeginTurn(t, req, pendingUserID)
	now := time.Now().UnixMilli()
	c.update(id, func(t *Thread) {
		resetTurnOutput(t)
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
		shown, images := attachmentDisplay(text, paths)
		t.Messages = append(t.Messages, Message{ID: pendingUserID, Role: "user", Content: shown, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, SentAtMs: now, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
		name, _, _ := strings.Cut(text, " ")
		if name == "/loop" || name == "/proactive" {
			setThreadTask(t, ThreadTask{ID: "loop:pending", Kind: "loop", Title: strings.TrimSpace(strings.TrimPrefix(text, name)), Status: "starting"})
		}
	})
	if c.claudeRuns == nil {
		c.claudeRuns = map[int]*claude.Client{}
	}
	existing := c.claudeRuns[id]
	generation := c.threadWithID(id).ClaudeGeneration
	if existing == nil {
		generation++
		c.update(id, func(t *Thread) { t.ClaudeGeneration = generation })
	}
	var executionEpoch string
	if execution := c.managedExecutions[req.ExecutionID]; execution != nil {
		if existing != nil {
			_, _, _, execution.epoch = existing.Runtime()
		} else {
			execution.epoch = "claude:" + claude.NewSessionID()
		}
		executionEpoch = execution.epoch
	}
	reject := func(err error) {
		c.dispatch(func() {
			if c.threadWithID(id).ClaudeGeneration != generation {
				return
			}
			if existing == nil {
				c.finishClaude(id, cwd, err)
				return
			}
			c.update(id, func(t *Thread) {
				t.Busy, t.TaskStopping = existing.Steerable(), false
				t.Status = err.Error()
				for i, m := range t.Messages {
					if m.ID == pendingUserID {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: err.Error()})
			})
			if req.ExecutionID == "" {
				c.returnDraft(id, text, paths)
			}
			state := dg.NeedsAttention
			if errors.Is(err, errManagedCancelled) {
				state = dg.Interrupted
			}
			if errors.Is(err, errManagedPaused) && strings.HasPrefix(req.ExecutionID, "delivery-") {
				s := c.collaboration
				s.post(func() {
					if e := s.manager.DeliveryOutcome(req.ExecutionID, "", "not_started", err.Error()); e == nil {
						s.release(req.ExecutionID)
					}
				})
			} else {
				c.managedEvent(req.ExecutionID, dg.Event{State: state, Result: dg.Result{Error: err.Error()}})
			}
		})
	}
	go func() {
		beforeInput := func(ctx context.Context) error {
			return c.persistManagedExecutionBinding(ctx, req.ExecutionID, ThreadRef{Source: c.referenceSource, Backend: agentClaude, SessionID: t.ServerID}.URL(), executionEpoch)
		}
		mem := c.memories().PrepareInput(t, req, cwd, c.threadRef(t).URL())
		content, err := claudeInput(collaborationTaggedInput(mem.Text(text), req.Parts), paths)
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
				err = existing.SendWithOptions(ctx, claude.Options{SessionID: t.ServerID, Content: content, Model: req.Model, Effort: req.Effort, PermissionMode: claudePermissionMode(policy), BeforeInput: beforeInput})
			}
			if err != nil {
				reject(err)
			}
			mem.Finish(err == nil)
			return
		}
		run, err := claude.Start(c.ctx, claude.Options{Binary: c.claudeBinary, Args: append(append([]string(nil), c.claudeArgs...), claudeMCPArgs(cwd)...), Env: append(append([]string(nil), c.claudeEnv...), claudeMCPEnv()...), Cwd: cwd, SessionID: t.ServerID, Resume: t.Resumed, PermissionMode: claudePermissionMode(policy), Model: req.Model, Effort: req.Effort, Content: content, BeforeInput: beforeInput}, func(f claude.Frame) {
			c.dispatch(func() {
				if c.threadWorkspace(id) == cwd && c.threadWithID(id).ClaudeGeneration == generation {
					c.handleClaude(id, f)
				}
			})
		}, func(err error) {
			c.dispatch(func() {
				if c.threadWithID(id).ClaudeGeneration == generation {
					c.finishClaude(id, cwd, err)
				}
			})
		})
		mem.Finish(err == nil)
		c.dispatch(func() {
			if c.threadWithID(id).ClaudeGeneration != generation {
				if run != nil {
					go run.Stop()
				}
				return
			}
			if err != nil {
				c.finishClaude(id, cwd, err)
				return
			}
			if c.threadWorkspace(id) != cwd {
				go run.Stop()
				return
			}
			c.claudeRuns[id] = run
			c.noteRuntime(c.threadWithID(id))
			if current := c.threadWithID(id); current.ID != 0 && current.StopRequested {
				go run.Stop()
			}
		})
	}()
	return id
}

func (c *chatController) finishClaude(id int, cwd string, err error) {
	defer func() {
		t := c.threadWithID(id)
		state := dg.Interrupted
		if !errors.Is(err, errManagedCancelled) && !t.StopRequested && (err != nil || t.Status != "") {
			state = dg.Failed
		}
		if errors.Is(err, errManagedPaused) && strings.HasPrefix(t.ExecutionID, "delivery-") {
			s := c.collaboration
			s.post(func() {
				if e := s.manager.DeliveryOutcome(t.ExecutionID, "", "not_started", err.Error()); e == nil {
					s.release(t.ExecutionID)
				}
			})
			return
		}
		c.managedClaudeResult(id, state, "")
	}()
	// The run's conversation may be in a background project.
	here := c.threadWorkspace(id) == cwd
	if here {
		sid := c.threadWithID(id).ServerID
		go func() {
			path := findClaudeSession(cwd, sid)
			if path == "" {
				return
			}
			session, ok := summarizeClaudeSession(path)
			if !ok {
				return
			}
			c.dispatch(func() {
				if c.threadWorkspace(id) != cwd {
					return
				}
				c.update(id, func(t *Thread) {
					if t.ServerID == sid {
						t.Title = session.Title
					}
				})
			})
		}()
	}
	delete(c.claudeRuns, id)
	if !here {
		return
	}
	c.update(id, func(t *Thread) {
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
		stopClaudeTasks(t, state)
		agentState := subagent.Interrupted
		if state == "failed" {
			agentState = subagent.Failed
		}
		interruptClaudeAgents(t, agentState)
		t.Status = ""
		if t.StopRequested {
			t.Status = "Stopped"
		} else if err != nil {
			t.Status = err.Error()
			t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: err.Error(), StartedAtMs: t.CompletedAtMs})
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
		completeClaudeTiming(t)
		saveClaudeHistory(*t)
	})
	pending := []codex.Approval{}
	prefix := fmt.Sprintf("\"claude:%d:", id)
	for _, a := range c.approvals() {
		if !strings.HasPrefix(string(a.ID), prefix) {
			pending = append(pending, a)
		}
	}
	c.setApprovals(pending)
}
func (c *chatController) handleClaude(id int, f claude.Frame) {
	if f.ParentToolUseID == "" && (f.Type == "assistant" || f.Type == "stream_event" || f.Type == "control_request") {
		c.managedClaudeStarted(id)
	}
	if f.Type == "assistant" || f.Type == "user" || f.Type == "system" || f.Type == "result" {
		c.noteRuntime(c.threadWithID(id))
	}
	if f.Type == "system" && f.Subtype == "init" {
		c.noteClaudeModel(f.Model)
		if f.Model != "" {
			c.update(id, func(t *Thread) { t.LaunchModel = f.Model })
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
			if run := c.claudeRuns[id]; run != nil {
				go run.Reply(f.RequestID, map[string]any{"behavior": "deny", "message": "Unsupported request"})
			}
			return
		}
		// Phones route request cards by the session ID, including requests
		// from conversations in background projects.
		a := codex.Approval{ID: rid, ThreadID: c.threadWithID(id).ServerID, ItemID: f.Request.ToolUseID, Method: "claude/permission", Command: f.Request.ToolName, Reason: string(f.Request.Input), Payload: f.Request.Input}
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
		c.setApprovals(append(append([]codex.Approval(nil), c.approvals()...), a))
		return
	}
	// Set only by a successful top-level result. The thread's Status is display
	// text that stays "running" until the process exits for ordinary turns, so
	// it cannot decide whether this turn completed.
	memoryTurnDone := false
	c.update(id, func(t *Thread) {
		defer func() { applyClaudeAgentTasks(t, f.AgentTasks) }()
		now := time.Now().UnixMilli()
		t.LastOutputAtMs = now
		if f.SchedulesKnown {
			applyClaudeSchedules(t, f.Schedules)
		}
		if f.Type == "command_lifecycle" && f.CommandState == "started" {
			if !t.Busy {
				resetTurnOutput(t)
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
			claudeTaskFrame(t, f)
			return
		}
		// A sub-agent's own frames are steps of its Task call, not messages
		// of the conversation.
		if f.ParentToolUseID != "" {
			if f.Type == "assistant" {
				claudeSubAgentStep(t, f.ParentToolUseID, f.Message.Content)
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
				recordClaudeOutput(t, e.Message.ID, e.Message.Usage)
			}
			if e.Type == "message_delta" {
				recordClaudeOutput(t, t.ClaudeMessageID, e.Usage)
			}
			if e.Type == "content_block_delta" && e.Delta.Type == "text_delta" {
				key := fmt.Sprintf("%s:%d", t.ClaudeMessageID, e.Index)
				for i := range t.Messages {
					if t.Messages[i].ItemID == key {
						t.Messages[i].Content += e.Delta.Text
						return
					}
				}
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "assistant", Kind: "agentMessage", ItemID: key, Content: e.Delta.Text, SentAtMs: now, StartedAtMs: now})
			}
		case "assistant":
			recordClaudeOutput(t, f.Message.ID, f.Message.Usage)
			for i, b := range f.Message.Content {
				if b.Type == "text" {
					key := claudeTextKey(t, f.Message.ID, i, b.Text)
					c.upsert(t, Message{Role: "assistant", Kind: "agentMessage", ItemID: key, Content: b.Text, SentAtMs: now, StartedAtMs: now})
				}
				if b.Type == "tool_use" {
					m := Message{Role: "tool", Kind: "claudeTool", ItemID: b.ID, Content: b.Name + "\n" + string(b.Input), Status: "inProgress", Agents: claudeSubAgent(b.ID, b.Name, b.Input, "inProgress")}
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
							output := claudeVisibleText(claudeBlocks(b.Content))
							if shown := claudeToolOutput(t.Messages[i], output); shown != "" {
								t.Messages[i].Content += "\n" + textutil.Bounded(shown)
							}
							claudeFinishSubAgents(&t.Messages[i], output, f.ToolUseResult)
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
			hadOutput := turnHasOutput(t.Messages)
			emptyResult := !f.IsError && !f.KeepingAlive && !hadOutput && strings.TrimSpace(f.Result) == ""
			if f.IsError || emptyResult {
				stopClaudeTasks(t, "failed")
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
				message = claudeErrorMessage(message, hadOutput, c.claudeCommand != "")
				t.Status = message
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: message, StartedAtMs: now})
			} else if !hadOutput && strings.TrimSpace(f.Result) != "" {
				// Some CLI transports only provide the final result text.
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "assistant", Kind: "agentMessage", Content: f.Result, SentAtMs: now, StartedAtMs: now})
			}
			if f.KeepingAlive && !f.WaitingForAgents {
				completeClaudeTiming(t)
				saveClaudeHistory(*t)
			}
			memoryTurnDone = !f.IsError && !emptyResult && !f.WaitingForAgents
		}
	})
	if memoryTurnDone {
		c.memories().CompletedTurn(id, f.UUID)
	}
	if f.Type == "result" && f.ParentToolUseID == "" && !f.WaitingForAgents {
		state := dg.Completed
		if c.threadWithID(id).StopRequested {
			state = dg.Interrupted
		} else if f.IsError {
			state = dg.Failed
		}
		c.managedClaudeResult(id, state, f.UUID)
	}
}
func (c *chatController) respondClaude(a codex.Approval, result any) bool {
	var id string
	_ = json.Unmarshal(a.ID, &id)
	if !strings.HasPrefix(id, "claude:") {
		return false
	}
	var thread int
	_, _ = fmt.Sscanf(id, "claude:%d:", &thread)
	parts := strings.SplitN(id, ":", 3)
	c.removeRequest(a.ID)
	run := c.claudeRuns[thread]
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

// claudePermissionMode maps the app's permission choices to Claude Code's
// modes, so one picker serves both agents.
func claudePermissionMode(policy string) string {
	switch normalizePermissionPolicy(policy) {
	case "never":
		return "bypassPermissions"
	case "on-failure":
		return "auto"
	default:
		return "manual"
	}
}

// turnHasOutput reports whether the turn since the last prompt produced a
// reply or a tool call.
func turnHasOutput(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "user":
			return false
		case "assistant", "tool":
			return true
		}
	}
	return false
}

// claudeErrorMessage explains an empty result, which Claude Code reports as
// a diagnostic such as "[ede_diagnostic] … stop_reason=null" when the
// request got no answer, keeping its own words below.
func claudeErrorMessage(message string, hadOutput, customCommand bool) string {
	if hadOutput && !strings.Contains(message, "ede_diagnostic") {
		return message
	}
	hint := "Claude Code returned no response. Check your connection and sign-in status, then try again."
	if customCommand {
		hint = "Claude Code returned no response. Check proxy or API settings in the custom launch command under Settings → General, or clear the command and try again."
	}
	return hint + "\n\nOriginal error: " + message
}

// claudeStallAfter is how long a run may send nothing before the app says
// it is still waiting.
const claudeStallAfter = 60 * time.Second

// stalledHint tells that Claude Code has sent nothing for a while. A tool
// that runs, or an approval the user has not answered, is not a stall.
func stalledHint(t Thread, waitingForUser bool, now time.Time) string {
	if !t.Busy || !backendOf(t).Caps().StallHint || t.LastOutputAtMs == 0 || waitingForUser {
		return ""
	}
	if n := len(t.Messages); n > 0 && t.Messages[n-1].Role == "tool" && t.Messages[n-1].Status == "inProgress" {
		return ""
	}
	quiet := now.Sub(time.UnixMilli(t.LastOutputAtMs))
	if quiet < claudeStallAfter {
		return ""
	}
	return fmt.Sprintf("No response for %s. Check your network or proxy, or stop and try again.", formatElapsed(quiet.Milliseconds()))
}

// claudeTextKey is the message a complete text block of a reply fills in.
// Claude Code sends each block of a reply in its own assistant frame, where
// it is at index 0, while streamed deltas carry the block's index in the
// whole reply; so the block completes the streamed message of its text, and
// an unstreamed block takes the first key no other text holds.
func claudeTextKey(t *Thread, messageID string, index int, text string) string {
	prefix := messageID + ":"
	partial := ""
	used := map[string]bool{}
	for _, m := range t.Messages {
		if m.Role != "assistant" || !strings.HasPrefix(m.ItemID, prefix) {
			continue
		}
		if m.Content == text {
			return m.ItemID
		}
		if partial == "" && m.Content != "" && strings.HasPrefix(text, m.Content) {
			partial = m.ItemID
		}
		used[m.ItemID] = true
	}
	if partial != "" {
		return partial
	}
	for k := index; ; k++ {
		if key := fmt.Sprintf("%s:%d", messageID, k); !used[key] {
			return key
		}
	}
}
