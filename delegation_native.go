package main

import (
	"errors"
	"strings"

	"miya/internal/codex"
	dg "miya/internal/delegation"
)

func (c *chatController) managedCodexAccepted(id, turn, epoch string, err error, mayHaveStarted bool) {
	if id == "" || c.collaboration == nil {
		return
	}
	run := c.collaboration.Execution(id)
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
		s.Post(func() {
			if saveErr := s.Manager.DeliveryOutcome(id, turn, outcome, reason); saveErr == nil {
				s.Release(id)
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
	run.Turn, run.Epoch = turn, epoch
	if task, readErr := c.collaboration.Manager.Read(id); readErr == nil && task.CancelRequested {
		c.update(run.Thread, func(t *Thread) { t.StopRequested = true })
	}
	state := dg.Running
	if run.PendingOverflow {
		state = dg.NeedsAttention
	}
	c.managedEvent(id, dg.Event{Target: c.threadRef(c.threadWithID(run.Thread)).URL(), NativeTurnID: turn, SessionEpoch: epoch, State: state})
	if event, ok := run.Pending[turn]; ok {
		c.managedEvent(id, event)
	}
	run.Pending = nil
}

func (c *chatController) managedCodexOutput(before Thread, method string, e codex.Event) {
	id := before.ExecutionID
	if id == "" || c.collaboration == nil {
		return
	}
	run := c.collaboration.Execution(id)
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
	if run.Turn != "" && run.Turn != turn {
		return
	}
	event := dg.Event{NativeTurnID: turn, SessionEpoch: run.Epoch, Target: c.threadRef(c.threadWithID(before.ID)).URL()}
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
	if run.Turn == "" {
		if run.PendingOverflow {
			return
		}
		if _, exists := run.Pending[turn]; !exists && len(run.Pending) >= 8 {
			run.PendingOverflow = true
			run.Pending = nil
			c.managedEvent(id, dg.Event{State: dg.NeedsAttention, Result: dg.Result{Error: "Too many unbound native turns arrived before submission acknowledgement; recheck the exact execution"}})
			return
		}
		if run.Pending == nil {
			run.Pending = map[string]dg.Event{}
		}
		run.Pending[turn] = event
		return
	}
	c.managedEvent(id, event)
}

// The adapter has not attempted turn/start or written a user input. A failed
// resume must not turn into an unknown notification or a user draft.
func (c *chatController) managedBeforeInputFailed(id, reason string) {
	if strings.HasPrefix(id, "delivery-") && c.collaboration != nil {
		s := c.collaboration
		s.Post(func() {
			if err := s.Manager.DeliveryOutcome(id, "", "not_started", reason); err == nil {
				s.Release(id)
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
	if run := c.collaboration.Execution(t.ExecutionID); run != nil {
		epoch = run.Epoch
	}
	c.managedEventWithOutcome(t.ExecutionID, dg.Event{Target: c.threadRef(t).URL(), SessionEpoch: epoch, NativeResultID: nativeResultID, State: state, Result: result}, nativeResultID != "")
}

func (c *chatController) managedClaudeStarted(id int) {
	t := c.threadWithID(id)
	run := c.collaboration.Execution(t.ExecutionID)
	if run == nil || run.Started {
		return
	}
	if task, err := c.collaboration.Manager.Read(t.ExecutionID); err == nil && task.CancelRequested {
		c.update(id, func(t *Thread) { t.StopRequested = true })
		if c.claudeRuns[id] != nil {
			c.stopAgentRun(t)
		}
	}
	run.Started = true
	c.managedEvent(t.ExecutionID, dg.Event{Target: c.threadRef(t).URL(), SessionEpoch: run.Epoch, State: dg.Running})
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
