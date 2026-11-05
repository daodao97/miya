package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	"miya/internal/model"
)

func (c *chatController) threadByReference(raw string) (Thread, bool) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok || ref.Source != c.referenceSource {
		return Thread{}, false
	}
	for _, t := range c.allThreads() {
		if c.threadRef(t) == ref {
			return t, true
		}
	}
	return Thread{}, false
}

func (c *chatController) prepareDelegation(caller Thread, a coordinator.Args) (dg.StartInput, error) {
	from := c.threadRef(caller).URL()
	if from == "" {
		return dg.StartInput{}, errors.New("CALLER_UNAVAILABLE: The initiating session is not created yet")
	}
	agent := a.Agent
	if agent == "" {
		agent = model.ThreadAgent(caller)
	}
	if agent = model.NormalizeAgentID(agent); agent == "" || !registeredBackend(agent) {
		return dg.StartInput{}, errors.New("INVALID_ARGUMENT: Unknown agent")
	}
	var target Thread
	if a.Thread != "" {
		var ok bool
		target, ok = c.threadByReference(a.Thread)
		if !ok {
			return dg.StartInput{}, errors.New("THREAD_NOT_FOUND: Target is unavailable")
		}
		agent = model.ThreadAgent(target)
		if c.threadWorkspace(target.ID) != c.threadWorkspace(caller.ID) {
			return dg.StartInput{}, errors.New("ACCESS_DENIED: Target belongs to another project")
		}
		if target.Busy || target.Loading || target.TaskPending || target.ReadOnly || len(target.Queue) > 0 {
			return dg.StartInput{}, errors.New("THREAD_BUSY: Target is not idle and writable")
		}
		if c.managedTargetUnavailable(target) {
			return dg.StartInput{}, errors.New("THREAD_BUSY: Target owns an active native goal or schedule")
		}
		if !target.HistoryLoaded {
			c.prefetchThread(target.ID)
			return dg.StartInput{}, errors.New("CONTEXT_LOADING: Target history is not loaded")
		}
	}
	b := c.agentStatus(agent)
	if !b.Ready {
		return dg.StartInput{}, fmt.Errorf("AGENT_UNAVAILABLE: %s is not connected", b.Name)
	}
	if a.Model != "" && !slices.Contains(b.ModelChoices, a.Model) {
		return dg.StartInput{}, errors.New("INVALID_ARGUMENT: Model is unavailable")
	}
	if a.Effort != "" && !slices.Contains(b.EffortChoices, a.Effort) {
		return dg.StartInput{}, errors.New("INVALID_ARGUMENT: Thinking option is unavailable")
	}
	if a.Context != "" && a.Context != "none" && a.Context != "caller_summary" {
		return dg.StartInput{}, errors.New("INVALID_ARGUMENT: Unsupported context selection")
	}
	if a.Notify != "" && a.Notify != "none" && a.Notify != "on_finish" {
		return dg.StartInput{}, errors.New("INVALID_ARGUMENT: Unsupported notification selection")
	}
	text := a.Task
	if a.Context == "caller_summary" {
		if !caller.HistoryLoaded {
			return dg.StartInput{}, errors.New("CONTEXT_LOADING: Caller history is not loaded")
		}
		text += "\n\n" + model.ReferenceText(c.threadRef(caller), threadRefTitle(caller))
	}
	expanded, _, err := expandThreadRefs(text, c.threadRef(target), c.resolveReference)
	if err != nil {
		return dg.StartInput{}, err
	}
	model, effort := a.Model, a.Effort
	if model == "" {
		model = b.Model
	}
	if effort == "" {
		effort = b.Effort
	}
	workspace := c.threadWorkspace(caller.ID)
	if target.ID != 0 {
		workspace = c.threadWorkspace(target.ID)
	}
	in := dg.StartInput{From: from, RequestID: a.RequestID, Target: a.Thread, Agent: agent, Workspace: workspace, Title: a.Title, Prompt: a.Task, Context: a.Context, Snapshot: expanded, Requested: dg.Config{Model: a.Model, Effort: a.Effort}, Effective: dg.Config{Model: model, Effort: effort, Policy: c.permissionForThread(target)}, Notify: a.Notify != "none", PreviousTaskID: a.PreviousTaskID}
	if caller.ExecutionID != "" && strings.HasPrefix(caller.ExecutionID, "task-") {
		in.ParentTaskID = caller.ExecutionID
	}
	if target.ID != 0 {
		key := from + "\n" + a.RequestID
		if reserved := c.collaboration.Reservation(target.ID); reserved != "" && reserved != key {
			return dg.StartInput{}, errors.New("THREAD_BUSY: Target is reserved")
		}
		if c.collaboration.Reservations == nil {
			c.collaboration.Reservations = map[int]string{}
		}
		c.collaboration.Reservations[target.ID] = key
	}
	return in, nil
}

// Called from an adapter goroutine before sending input to a newly created
// native session. Persisting its identity cannot block the UI executor.
func (c *chatController) persistManagedBinding(ctx context.Context, id, target string) error {
	return c.persistManagedExecutionBinding(ctx, id, target, "")
}

func (c *chatController) persistManagedExecutionBinding(ctx context.Context, id, target, epoch string) error {
	return c.collaboration.BindExecution(ctx, id, target, epoch)
}

func (c *chatController) managedEvent(id string, event dg.Event) {
	c.managedEventWithOutcome(id, event, false)
}

func (c *chatController) managedEventWithOutcome(id string, event dg.Event, saveOutcome bool) {
	c.collaboration.Observe(id, event, saveOutcome)
}
