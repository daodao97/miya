package main

import (
	"errors"
	"strings"

	"miya/internal/codex"
	dg "miya/internal/delegation"
	"miya/internal/subagent"
)

func (c *chatController) managedCodexAccepted(id, turn, epoch string, err error, mayHaveStarted bool) {
	if id == "" || c.collaboration == nil {
		return
	}
	run := c.collaboration.execution(id)
	if run == nil {
		return
	}
	if err == nil && turn == "" {
		err = errors.New("Native start returned no turn identity")
		mayHaveStarted = true
	}
	if strings.HasPrefix(id, "delivery-") {
		outcome := "accepted"
		reason := ""
		if err != nil {
			reason = err.Error()
			outcome = "unknown"
			if !mayHaveStarted || provenCodexRejection(err) {
				outcome = "not_started"
			}
		}
		s := c.collaboration
		s.post(func() {
			if saveErr := s.manager.DeliveryOutcome(id, turn, outcome, reason); saveErr == nil {
				s.release(id)
			}
		})
		return
	}
	if err != nil {
		state := dg.NeedsAttention
		if !mayHaveStarted || provenCodexRejection(err) {
			state = dg.Failed
		}
		if errors.Is(err, errManagedCancelled) {
			state = dg.Interrupted
		}
		c.managedEvent(id, dg.Event{State: state, SessionEpoch: epoch, Result: dg.Result{Error: err.Error()}})
		return
	}
	run.turn, run.epoch = turn, epoch
	if task, readErr := c.collaboration.manager.Read(id); readErr == nil && task.CancelRequested {
		c.update(run.thread, func(t *Thread) { t.StopRequested = true })
	}
	state := dg.Running
	if run.pendingOverflow {
		state = dg.NeedsAttention
	}
	c.managedEvent(id, dg.Event{Target: c.threadRef(c.threadWithID(run.thread)).URL(), NativeTurnID: turn, SessionEpoch: epoch, State: state})
	if event, ok := run.pending[turn]; ok {
		c.managedEvent(id, event)
	}
	run.pending = nil
}

// Generic native/internal errors can occur after side effects. Only protocol
// validation/method rejection proves a sent start request was not accepted.
func provenCodexRejection(err error) bool {
	var rpc *codex.RPCError
	return errors.As(err, &rpc) && (rpc.Code == -32600 || rpc.Code == -32601 || rpc.Code == -32602)
}

func (c *chatController) managedCodexOutput(before Thread, method string, e codex.Event) {
	id := before.ExecutionID
	if id == "" || c.collaboration == nil {
		return
	}
	run := c.collaboration.execution(id)
	if run == nil {
		return
	}
	turn := e.TurnID
	if turn == "" {
		turn = e.Turn.ID
	}
	if turn == "" {
		return
	}
	if run.turn != "" && run.turn != turn {
		return
	}
	event := dg.Event{NativeTurnID: turn, SessionEpoch: run.epoch, Target: c.threadRef(c.threadWithID(before.ID)).URL()}
	if method == "turn/completed" {
		switch e.Turn.Status {
		case "completed":
			event.State = dg.Completed
		case "failed":
			event.State = dg.Failed
		case "interrupted":
			event.State = dg.Interrupted
		default:
			return
		}
		var messages []Message
		for _, m := range c.threadWithID(before.ID).Messages {
			if m.TurnID == turn {
				messages = append(messages, m)
			}
		}
		event.Result = managedResult(messages, true)
		if e.Turn.Error != nil {
			event.Result.Error = e.Turn.Error.Message
		}
	} else {
		return
	}
	if run.turn == "" {
		if run.pendingOverflow {
			return
		}
		if _, exists := run.pending[turn]; !exists && len(run.pending) >= 8 {
			run.pendingOverflow = true
			run.pending = nil
			c.managedEvent(id, dg.Event{State: dg.NeedsAttention, Result: dg.Result{Error: "Too many unbound native turns arrived before submission acknowledgement; recheck the exact execution"}})
			return
		}
		if run.pending == nil {
			run.pending = map[string]dg.Event{}
		}
		run.pending[turn] = event
		return
	}
	c.managedEvent(id, event)
}

func managedResult(messages []Message, phases bool) dg.Result {
	r := dg.Result{Artifacts: changedFiles(messages)}
	for _, m := range messages {
		if m.Role == "error" {
			r.Error = m.Content
		}
		if m.Role != "assistant" || strings.TrimSpace(m.Content) == "" {
			continue
		}
		if phases && m.Item != nil && !codexFinalPhase(m.Item.Phase) {
			continue
		}
		r.Text = m.Content
	}
	return r
}

func codexFinalPhase(phase string) bool { return phase == "" || phase == "final_answer" }

// The adapter has not attempted turn/start or written a user input. A failed
// resume must not turn into an unknown notification or a user draft.
func (c *chatController) managedBeforeInputFailed(id, reason string) {
	if strings.HasPrefix(id, "delivery-") && c.collaboration != nil {
		s := c.collaboration
		s.post(func() {
			if err := s.manager.DeliveryOutcome(id, "", "not_started", reason); err == nil {
				s.release(id)
			}
		})
		return
	}
	c.managedEvent(id, dg.Event{State: dg.Failed, Result: dg.Result{Error: reason}})
}

func (c *chatController) managedClaudeResult(id int, state dg.State, nativeResultID string) {
	t := c.threadWithID(id)
	if t.ExecutionID == "" || c.collaboration == nil {
		return
	}
	var messages []Message
	for _, m := range t.Messages {
		if m.ExecutionID == t.ExecutionID {
			messages = append(messages, m)
		}
	}
	result := managedResult(messages, false)
	if result.Error != "" && state == dg.Completed {
		state = dg.Failed
	}
	epoch := ""
	if run := c.collaboration.execution(t.ExecutionID); run != nil {
		epoch = run.epoch
	}
	c.managedEventWithOutcome(t.ExecutionID, dg.Event{Target: c.threadRef(t).URL(), SessionEpoch: epoch, NativeResultID: nativeResultID, State: state, Result: result}, nativeResultID != "")
}

func (c *chatController) managedClaudeStarted(id int) {
	t := c.threadWithID(id)
	run := c.collaboration.execution(t.ExecutionID)
	if run == nil || run.started {
		return
	}
	if task, err := c.collaboration.manager.Read(t.ExecutionID); err == nil && task.CancelRequested {
		c.update(id, func(t *Thread) { t.StopRequested = true })
		if c.claudeRuns[id] != nil {
			c.stopAgentRun(t)
		}
	}
	run.started = true
	c.managedEvent(t.ExecutionID, dg.Event{Target: c.threadRef(t).URL(), SessionEpoch: run.epoch, State: dg.Running})
}

func managedLongRunning(t Thread) bool {
	for _, task := range t.Tasks {
		if task.Kind == "goal" && task.Status == "active" {
			return true
		}
		if task.Kind == "loop" && (task.Status == "waiting" || task.Status == "running" || task.Status == "active" || task.Status == "starting") {
			return true
		}
	}
	for _, m := range t.Messages {
		for _, agent := range m.Agents {
			if subagent.Active(agent.Status) || agent.Status == subagent.Unconfirmed {
				return true
			}
		}
	}
	return false
}

func (c *chatController) managedTargetUnavailable(t Thread) bool {
	if managedLongRunning(t) {
		return true
	}
	// A Claude process held open after the root reply owns native background
	// events. Until their input/result identity can be separated, a managed
	// submit must not borrow that stream and stamp all its frames with a task ID.
	if run := c.claudeRuns[t.ID]; run != nil {
		select {
		case <-run.Done():
		default:
			return true
		}
	}
	return false
}
