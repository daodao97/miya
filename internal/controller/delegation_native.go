package controller

import (
	"errors"
	"strings"

	"miya/internal/codex"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	"miya/internal/model"
)

func (c *Controller) ManagedCodexAccepted(id, turn, epoch string, err error, mayHaveStarted bool) {
	if id == "" || c.Collaboration == nil {
		return
	}
	run := c.Collaboration.Execution(id)
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
			if !mayHaveStarted || coordinator.ProvenCodexRejection(err) {
				outcome = "not_started"
			}
		}
		s := c.Collaboration
		s.Post(func() {
			if saveErr := s.Manager.DeliveryOutcome(id, turn, outcome, reason); saveErr == nil {
				s.Release(id)
			}
		})
		return
	}
	if err != nil {
		state := dg.NeedsAttention
		if !mayHaveStarted || coordinator.ProvenCodexRejection(err) {
			state = dg.Failed
		}
		if errors.Is(err, errManagedCancelled) {
			state = dg.Interrupted
		}
		c.ManagedEvent(id, dg.Event{State: state, SessionEpoch: epoch, Result: dg.Result{Error: err.Error()}})
		return
	}
	run.Turn, run.Epoch = turn, epoch
	if task, readErr := c.Collaboration.Manager.Read(id); readErr == nil && task.CancelRequested {
		c.Update(run.Thread, func(t *model.Thread) { t.StopRequested = true })
	}
	state := dg.Running
	if run.PendingOverflow {
		state = dg.NeedsAttention
	}
	c.ManagedEvent(id, dg.Event{Target: c.ThreadRef(c.ThreadWithID(run.Thread)).URL(), NativeTurnID: turn, SessionEpoch: epoch, State: state})
	if event, ok := run.Pending[turn]; ok {
		c.ManagedEvent(id, event)
	}
	run.Pending = nil
}

func (c *Controller) ManagedCodexOutput(before model.Thread, method string, e codex.Event) {
	id := before.ExecutionID
	if id == "" || c.Collaboration == nil {
		return
	}
	run := c.Collaboration.Execution(id)
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
	event := dg.Event{NativeTurnID: turn, SessionEpoch: run.Epoch, Target: c.ThreadRef(c.ThreadWithID(before.ID)).URL()}
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
		var messages []model.Message
		for _, m := range c.ThreadWithID(before.ID).Messages {
			if m.TurnID == turn {
				messages = append(messages, m)
			}
		}
		event.Result = coordinator.NativeResult(messages, true)
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
			c.ManagedEvent(id, dg.Event{State: dg.NeedsAttention, Result: dg.Result{Error: "Too many unbound native turns arrived before submission acknowledgement; recheck the exact execution"}})
			return
		}
		if run.Pending == nil {
			run.Pending = map[string]dg.Event{}
		}
		run.Pending[turn] = event
		return
	}
	c.ManagedEvent(id, event)
}

// The adapter has not attempted turn/start or written a user input. A failed
// resume must not turn into an unknown notification or a user draft.
func (c *Controller) managedBeforeInputFailed(id, reason string) {
	if strings.HasPrefix(id, "delivery-") && c.Collaboration != nil {
		s := c.Collaboration
		s.Post(func() {
			if err := s.Manager.DeliveryOutcome(id, "", "not_started", reason); err == nil {
				s.Release(id)
			}
		})
		return
	}
	c.ManagedEvent(id, dg.Event{State: dg.Failed, Result: dg.Result{Error: reason}})
}

func (c *Controller) managedClaudeResult(id int, state dg.State, nativeResultID string) {
	t := c.ThreadWithID(id)
	if t.ExecutionID == "" || c.Collaboration == nil {
		return
	}
	var messages []model.Message
	for _, m := range t.Messages {
		if m.ExecutionID == t.ExecutionID {
			messages = append(messages, m)
		}
	}
	result := coordinator.NativeResult(messages, false)
	if result.Error != "" && state == dg.Completed {
		state = dg.Failed
	}
	epoch := ""
	if run := c.Collaboration.Execution(t.ExecutionID); run != nil {
		epoch = run.Epoch
	}
	c.managedEventWithOutcome(t.ExecutionID, dg.Event{Target: c.ThreadRef(t).URL(), SessionEpoch: epoch, NativeResultID: nativeResultID, State: state, Result: result}, nativeResultID != "")
}

func (c *Controller) managedClaudeStarted(id int) {
	t := c.ThreadWithID(id)
	run := c.Collaboration.Execution(t.ExecutionID)
	if run == nil || run.Started {
		return
	}
	if task, err := c.Collaboration.Manager.Read(t.ExecutionID); err == nil && task.CancelRequested {
		c.Update(id, func(t *model.Thread) { t.StopRequested = true })
		if c.ClaudeRuns[id] != nil {
			c.stopAgentRun(t)
		}
	}
	run.Started = true
	c.ManagedEvent(t.ExecutionID, dg.Event{Target: c.ThreadRef(t).URL(), SessionEpoch: run.Epoch, State: dg.Running})
}

func (c *Controller) managedTargetUnavailable(t model.Thread) bool {
	if coordinator.LongRunning(t) {
		return true
	}
	// A Claude process held open after the root reply owns native background
	// events. Until their input/result identity can be separated, a managed
	// submit must not borrow that stream and stamp all its frames with a task ID.
	if run := c.ClaudeRuns[t.ID]; run != nil {
		select {
		case <-run.Done():
		default:
			return true
		}
	}
	return false
}
