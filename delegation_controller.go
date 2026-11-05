package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"miya/internal/async"
	"miya/internal/claude"
	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/textutil"
)

var errManagedCancelled = errors.New("Cancelled before native input was submitted")
var errManagedPaused = errors.New("Collaboration delivery paused before native input was submitted")

type managedExecution struct {
	thread              int
	turn, epoch         string
	sequence            uint64
	started             bool
	pending             map[string]dg.Event
	pendingOverflow     bool
	progressAt          time.Time
	progress, waitCause string
}

// The worker serializes durable operations without blocking the controller's
// executor. Native callbacks only enqueue immutable events on this worker.
type collaborationState struct {
	c              *chatController
	manager        *dg.Manager
	loadError      error
	worker         *async.Executor
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	busy           atomic.Int64
	dispatched     map[string]bool // worker-owned
	nextCollection time.Time
	cleanupError   atomic.Pointer[string]
	// Main-thread owned: the conversation each execution reserved, the
	// executions in progress, and the receivers whose delivery is paused.
	reservations map[int]string
	executions   map[string]*managedExecution
	pausedRefs   map[string]bool
	pauseGen     map[string]uint64
}

// A window has no collaboration, so these read nothing on a nil state.

func (s *collaborationState) reservation(thread int) string {
	if s == nil {
		return ""
	}
	return s.reservations[thread]
}

func (s *collaborationState) execution(id string) *managedExecution {
	if s == nil {
		return nil
	}
	return s.executions[id]
}

func (s *collaborationState) isPaused(ref string) bool {
	return s != nil && s.pausedRefs[ref]
}

func (s *collaborationState) pauseGeneration(ref string) uint64 {
	if s == nil {
		return 0
	}
	return s.pauseGen[ref]
}

func newCollaboration(c *chatController, dir string) *collaborationState {
	ctx, cancel := context.WithCancel(c.parentContext())
	s := &collaborationState{c: c, worker: async.NewExecutor(), ctx: ctx, cancel: cancel, done: make(chan struct{}), dispatched: map[string]bool{}}
	s.manager, s.loadError = dg.Open(dg.NewStore(dir), dg.Options{})
	if s.loadError == nil {
		s.loadError = s.manager.Recover()
		s.pausedRefs = s.manager.View().Paused
	}
	go func() { defer close(s.done); s.worker.Loop(ctx.Done()) }()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.post(s.tick)
			}
		}
	}()
	return s
}

func (s *collaborationState) post(f func()) {
	if s == nil {
		return
	}
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	s.busy.Add(1)
	s.worker.Push(func() {
		defer s.busy.Add(-1)
		var revision uint64
		var health string
		cleanup := s.cleanupError.Load()
		if s.manager != nil {
			revision, health = s.manager.Revision(), s.manager.StorageError()
		}
		f()
		if s.manager != nil && (revision != s.manager.Revision() || health != s.manager.StorageError() || cleanup != s.cleanupError.Load()) {
			s.c.dispatch(func() {
				if s.ctx.Err() == nil {
					invalidate()
				}
			})
		}
	})
}
func (s *collaborationState) call(f func() (any, error)) (any, error) {
	if s.loadError != nil {
		return nil, fmt.Errorf("STORAGE_UNAVAILABLE: %w", s.loadError)
	}
	type reply struct {
		v   any
		err error
	}
	done := make(chan reply, 1)
	s.post(func() { v, err := f(); done <- reply{v, err} })
	select {
	case r := <-done:
		return r.v, r.err
	case <-s.ctx.Done():
		return nil, errors.New("Collaboration service stopped")
	}
}
func (s *collaborationState) onMain(f func() (any, error)) (any, error) {
	type reply struct {
		v   any
		err error
	}
	r, ok := async.OnMain(s.ctx, s.c.dispatch, func() reply {
		if s.ctx.Err() != nil {
			return reply{err: s.ctx.Err()}
		}
		v, err := f()
		return reply{v, err}
	})
	if !ok {
		return nil, s.ctx.Err()
	}
	return r.v, r.err
}

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

func (c *chatController) prepareDelegation(caller Thread, a collaborationArgs) (dg.StartInput, error) {
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
		if reserved := c.collaboration.reservation(target.ID); reserved != "" && reserved != key {
			return dg.StartInput{}, errors.New("THREAD_BUSY: Target is reserved")
		}
		if c.collaboration.reservations == nil {
			c.collaboration.reservations = map[int]string{}
		}
		c.collaboration.reservations[target.ID] = key
	}
	return in, nil
}

func (s *collaborationState) create(in dg.StartInput) (dg.Task, error) {
	task, err := s.manager.Create(in)
	if err != nil {
		_, _ = s.onMain(func() (any, error) {
			if t, ok := s.c.threadByReference(in.Target); ok && s.c.collaboration.reservation(t.ID) == in.From+"\n"+in.RequestID {
				delete(s.c.collaboration.reservations, t.ID)
				s.c.drainQueue(t.ID)
			}
			return nil, nil
		})
		return dg.Task{}, err
	}
	if task.State == dg.Starting && !s.dispatched[task.ID] && s.manager.StorageError() == "" {
		s.start(task)
	}
	return s.manager.Read(task.ID)
}

func (s *collaborationState) start(task dg.Task) {
	if s.dispatched[task.ID] || s.ctx.Err() != nil {
		return
	}
	if latest, err := s.manager.Read(task.ID); err == nil && latest.CancelRequested {
		_ = s.manager.Observe(dg.Event{TaskID: task.ID, State: dg.Interrupted, Result: dg.Result{Error: "Cancelled before native submission"}})
		s.release(task.ID)
		return
	}
	s.dispatched[task.ID] = true
	prepared, err := s.onMain(func() (any, error) {
		c := s.c
		t := Thread{}
		if task.Target != "" {
			var ok bool
			t, ok = c.threadByReference(task.Target)
			if !ok {
				return nil, errors.New("Target session is unavailable")
			}
			if t.Busy || t.Loading || t.ReadOnly || t.TaskPending || c.managedTargetUnavailable(t) {
				return nil, errors.New("Target session is busy or read-only")
			}
			reserved := c.collaboration.reservation(t.ID)
			if reserved != "" && reserved != task.ID && reserved != task.From+"\n"+task.RequestID {
				return nil, errors.New("Target session is reserved by another submission")
			}
		} else {
			title := task.Title
			if strings.TrimSpace(title) == "" {
				title = textutil.ClipRunes(textutil.FirstLine(task.Prompt), 120)
			}
			parent, _ := c.threadByReference(task.From)
			title += " · " + threadRefTitle(parent)
			t = Thread{ID: c.nextID(), Agent: task.Agent, Title: title, Project: filepath.Base(task.Workspace), Loaded: true, HistoryLoaded: true, PermissionPolicy: task.Config.Policy}
			if task.Agent == agentClaude {
				t.ServerID = claude.NewSessionID()
			}
			c.addThread(task.Workspace, t)
		}
		if !c.agentStatus(task.Agent).Ready {
			return nil, errors.New("Target agent is not ready")
		}
		if c.collaboration.reservations == nil {
			c.collaboration.reservations = map[int]string{}
		}
		c.collaboration.reservations[t.ID] = task.ID
		if c.collaboration.executions == nil {
			c.collaboration.executions = map[string]*managedExecution{}
		}
		c.collaboration.executions[task.ID] = &managedExecution{thread: t.ID, pending: map[string]dg.Event{}}
		return t, nil
	})
	if err != nil {
		s.failBeforeStart(task, err)
		return
	}
	t := prepared.(Thread)
	if t.ServerID != "" {
		if err := s.manager.Observe(dg.Event{TaskID: task.ID, Target: ThreadRef{Source: referenceSourceOf(task.From), Backend: task.Agent, SessionID: t.ServerID}.URL()}); err != nil {
			s.release(task.ID)
			return
		}
	}
	_, err = s.onMain(func() (any, error) {
		c := s.c
		current := c.threadWithID(t.ID)
		if current.Busy || current.Loading || current.ReadOnly || c.managedTargetUnavailable(current) {
			return nil, errors.New("Target changed before submission")
		}
		text := task.Snapshot
		if text == "" {
			text = task.Prompt
		}
		id := c.submitExpanded(SendRequest{ExecutionID: task.ID, Origin: "delegation", ThreadID: t.ID, Agent: task.Agent, Project: task.Workspace, Text: text, Model: task.Config.Model, Effort: task.Config.Effort, Policy: task.Config.Policy, ExplicitSettings: true, MemoryControl: true})
		if id == 0 {
			return nil, errors.New("Native submission was not started")
		}
		return nil, nil
	})
	if err != nil {
		s.failBeforeStart(task, err)
	}
}

func referenceSourceOf(raw string) string { ref, _ := model.ParseThreadURL(raw); return ref.Source }
func (s *collaborationState) failBeforeStart(task dg.Task, err error) {
	if saveErr := s.manager.Observe(dg.Event{TaskID: task.ID, State: dg.Failed, Result: dg.Result{Error: err.Error()}}); saveErr != nil {
		return
	}
	s.release(task.ID)
}
func (s *collaborationState) release(id string) {
	_, _ = s.onMain(func() (any, error) {
		c := s.c
		execution := c.collaboration.execution(id)
		if execution == nil {
			if task, err := s.manager.Read(id); err == nil {
				if t, ok := c.threadByReference(task.Target); ok && c.collaboration.reservation(t.ID) == task.From+"\n"+task.RequestID {
					delete(c.collaboration.reservations, t.ID)
					c.drainQueue(t.ID)
				}
			}
		}
		if execution != nil {
			if c.collaboration.reservation(execution.thread) == id {
				delete(c.collaboration.reservations, execution.thread)
			}
			c.update(execution.thread, func(t *Thread) {
				if t.ExecutionID == id {
					t.ExecutionID = ""
				}
			})
			delete(c.collaboration.executions, id)
			c.drainQueue(execution.thread)
		}
		return nil, nil
	})
}
func (s *collaborationState) tick() {
	if s.loadError != nil || s.ctx.Err() != nil {
		return
	}
	s.checkProgress()
	if err := s.manager.ExpireDeliveries(); err != nil {
		return
	}
	if now := time.Now(); !now.Before(s.nextCollection) {
		if err := s.manager.Collect(); err != nil {
			message := err.Error()
			s.cleanupError.Store(&message)
		} else {
			s.cleanupError.Store(nil)
		}
		s.nextCollection = now.Add(time.Hour)
	}
	tasks, err := s.manager.ClaimQueued()
	if err != nil {
		return
	}
	for _, t := range tasks {
		s.start(t)
	}
	s.deliverPending()
}

// Called from an adapter goroutine before sending input to a newly created
// native session. Persisting its identity cannot block the UI executor.
func (c *chatController) persistManagedBinding(ctx context.Context, id, target string) error {
	return c.persistManagedExecutionBinding(ctx, id, target, "")
}

func (c *chatController) persistManagedExecutionBinding(ctx context.Context, id, target, epoch string) error {
	if id == "" || c.collaboration == nil {
		return nil
	}
	s := c.collaboration
	if strings.HasPrefix(id, "delivery-") {
		if err := s.manager.StorageError(); err != "" {
			return fmt.Errorf("STORAGE_UNAVAILABLE: %s", err)
		}
		d, ok := s.manager.Delivery(id)
		if !ok || d.Notify != target || d.State != "submitting" {
			return errors.New("Notification target does not match its persisted receiver")
		}
		var allowed bool
		_, err := s.onMain(func() (any, error) {
			t, found := c.threadByReference(target)
			allowed = found && !c.collaboration.isPaused(target) && c.collaboration.reservation(t.ID) == id && t.ExecutionID == id && !t.StopRequested
			return nil, nil
		})
		if err != nil {
			return err
		}
		if !allowed {
			return errManagedPaused
		}
		_, err = s.call(func() (any, error) { return nil, s.manager.BeginDeliveryInput(id) })
		if err != nil {
			return err
		}
		// Stop may have arrived while the input intent was syncing.
		_, err = s.onMain(func() (any, error) {
			t, found := c.threadByReference(target)
			allowed = found && !c.collaboration.isPaused(target) && c.collaboration.reservation(t.ID) == id && t.ExecutionID == id && !t.StopRequested
			return nil, nil
		})
		if err == nil && !allowed {
			err = errManagedPaused
		}
		return err
	}
	done := make(chan error, 1)
	s.post(func() {
		task, err := s.manager.Read(id)
		if err == nil && task.CancelRequested {
			err = errManagedCancelled
		}
		if err == nil {
			err = s.manager.Observe(dg.Event{TaskID: id, Target: target, SessionEpoch: epoch, SubmissionPhase: "submitting"})
		}
		done <- err
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (c *chatController) managedEvent(id string, event dg.Event) {
	c.managedEventWithOutcome(id, event, false)
}

func (c *chatController) managedEventWithOutcome(id string, event dg.Event, saveOutcome bool) {
	if id == "" || c.collaboration == nil {
		return
	}
	s := c.collaboration
	if strings.HasPrefix(id, "delivery-") {
		outcome := "unknown"
		if event.State == dg.Running || event.State == dg.Completed {
			outcome = "accepted"
		}
		s.post(func() {
			if d, ok := s.manager.Delivery(id); ok && d.SubmissionPhase == "prepared" && outcome == "unknown" {
				outcome = "not_started"
			} else if ok && d.SubmissionPhase == "submitting" && event.NativeResultID != "" {
				// A root result (including a failed reply) proves the bound
				// Claude process accepted input; delivery is not reply success.
				outcome = "accepted"
			}
			if err := s.manager.DeliveryOutcome(id, event.NativeTurnID, outcome, event.Result.Error); err == nil {
				s.release(id)
			}
		})
		return
	}
	if run := c.collaboration.execution(id); run != nil {
		run.sequence++
		event.Sequence = run.sequence
	}
	event.TaskID = id
	s.post(func() {
		if saveOutcome {
			if err := s.manager.RecordNativeOutcome(event); err != nil {
				return
			}
		}
		if err := s.manager.Observe(event); err != nil {
			return
		}
		if dg.Terminal(event.State) {
			s.release(id)
		}
		s.tick()
	})
}
