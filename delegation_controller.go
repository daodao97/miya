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
	Thread              int
	Turn, Epoch         string
	Sequence            uint64
	Started             bool
	Pending             map[string]dg.Event
	PendingOverflow     bool
	progressAt          time.Time
	progress, waitCause string
}

// The worker serializes durable operations without blocking the controller's
// executor. Native callbacks only enqueue immutable events on this worker.
type collaborationState struct {
	host           collaborationHost
	Manager        *dg.Manager
	LoadError      error
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
	Reservations map[int]string
	Executions   map[string]*managedExecution
	pausedRefs   map[string]bool
	pauseGen     map[string]uint64
}

// A window has no collaboration, so these read nothing on a nil state.

func (s *collaborationState) Reservation(thread int) string {
	if s == nil {
		return ""
	}
	return s.Reservations[thread]
}

func (s *collaborationState) Execution(id string) *managedExecution {
	if s == nil {
		return nil
	}
	return s.Executions[id]
}

func (s *collaborationState) IsPaused(ref string) bool {
	return s != nil && s.pausedRefs[ref]
}

// Context ends when the collaboration stops.
func (s *collaborationState) Context() context.Context { return s.ctx }

// Stop ends the collaboration's work; Done is closed once its worker
// returned.
func (s *collaborationState) Stop()                 { s.cancel() }
func (s *collaborationState) Done() <-chan struct{} { return s.done }

// Pending is how much collaboration work is queued or running.
func (s *collaborationState) Pending() int64 { return s.busy.Load() }

// CleanupError is why the last cleanup of finished tasks failed, "" when
// it did not.
func (s *collaborationState) CleanupError() string {
	if message := s.cleanupError.Load(); message != nil {
		return *message
	}
	return ""
}

// StartRecheck rechecks a task's native outcome in the background, as
// pending work.
func (s *collaborationState) StartRecheck(caller, id string) {
	s.busy.Add(1)
	go func() { defer s.busy.Add(-1); s.RecheckTask(caller, id) }()
}

func (s *collaborationState) pauseGeneration(ref string) uint64 {
	if s == nil {
		return 0
	}
	return s.pauseGen[ref]
}

func newCollaboration(c *chatController, dir string) *collaborationState {
	ctx, cancel := context.WithCancel(c.parentContext())
	s := &collaborationState{host: collaborationHostOf{c}, worker: async.NewExecutor(), ctx: ctx, cancel: cancel, done: make(chan struct{}), dispatched: map[string]bool{}}
	s.Manager, s.LoadError = dg.Open(dg.NewStore(dir), dg.Options{})
	if s.LoadError == nil {
		s.LoadError = s.Manager.Recover()
		s.pausedRefs = s.Manager.View().Paused
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
				s.Post(s.Tick)
			}
		}
	}()
	return s
}

func (s *collaborationState) Post(f func()) {
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
		if s.Manager != nil {
			revision, health = s.Manager.Revision(), s.Manager.StorageError()
		}
		f()
		if s.Manager != nil && (revision != s.Manager.Revision() || health != s.Manager.StorageError() || cleanup != s.cleanupError.Load()) {
			s.host.Dispatch(func() {
				if s.ctx.Err() == nil {
					s.host.Invalidate()
				}
			})
		}
	})
}
func (s *collaborationState) Call(f func() (any, error)) (any, error) {
	if s.LoadError != nil {
		return nil, fmt.Errorf("STORAGE_UNAVAILABLE: %w", s.LoadError)
	}
	type reply struct {
		v   any
		err error
	}
	done := make(chan reply, 1)
	s.Post(func() { v, err := f(); done <- reply{v, err} })
	select {
	case r := <-done:
		return r.v, r.err
	case <-s.ctx.Done():
		return nil, errors.New("Collaboration service stopped")
	}
}
func (s *collaborationState) OnMain(f func() (any, error)) (any, error) {
	type reply struct {
		v   any
		err error
	}
	r, ok := async.OnMain(s.ctx, s.host.Dispatch, func() reply {
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

func (s *collaborationState) Create(in dg.StartInput) (dg.Task, error) {
	task, err := s.Manager.Create(in)
	if err != nil {
		_, _ = s.OnMain(func() (any, error) {
			if t, ok := s.host.ThreadByRef(in.Target); ok && s.Reservation(t.ID) == in.From+"\n"+in.RequestID {
				delete(s.Reservations, t.ID)
				s.host.DrainQueue(t.ID)
			}
			return nil, nil
		})
		return dg.Task{}, err
	}
	if task.State == dg.Starting && !s.dispatched[task.ID] && s.Manager.StorageError() == "" {
		s.start(task)
	}
	return s.Manager.Read(task.ID)
}

func (s *collaborationState) start(task dg.Task) {
	if s.dispatched[task.ID] || s.ctx.Err() != nil {
		return
	}
	if latest, err := s.Manager.Read(task.ID); err == nil && latest.CancelRequested {
		_ = s.Manager.Observe(dg.Event{TaskID: task.ID, State: dg.Interrupted, Result: dg.Result{Error: "Cancelled before native submission"}})
		s.Release(task.ID)
		return
	}
	s.dispatched[task.ID] = true
	prepared, err := s.OnMain(func() (any, error) {
		t := Thread{}
		if task.Target != "" {
			var ok bool
			t, ok = s.host.ThreadByRef(task.Target)
			if !ok {
				return nil, errors.New("Target session is unavailable")
			}
			if t.Busy || t.Loading || t.ReadOnly || t.TaskPending || s.host.TargetUnavailable(t) {
				return nil, errors.New("Target session is busy or read-only")
			}
			reserved := s.Reservation(t.ID)
			if reserved != "" && reserved != task.ID && reserved != task.From+"\n"+task.RequestID {
				return nil, errors.New("Target session is reserved by another submission")
			}
		} else {
			title := task.Title
			if strings.TrimSpace(title) == "" {
				title = textutil.ClipRunes(textutil.FirstLine(task.Prompt), 120)
			}
			parent, _ := s.host.ThreadByRef(task.From)
			title += " · " + s.host.ThreadTitle(parent)
			t = Thread{ID: s.host.NextID(), Agent: task.Agent, Title: title, Project: filepath.Base(task.Workspace), Loaded: true, HistoryLoaded: true, PermissionPolicy: task.Config.Policy}
			if task.Agent == agentClaude {
				t.ServerID = claude.NewSessionID()
			}
			s.host.AddThread(task.Workspace, t)
		}
		if !s.host.Agent(task.Agent).Ready {
			return nil, errors.New("Target agent is not ready")
		}
		if s.Reservations == nil {
			s.Reservations = map[int]string{}
		}
		s.Reservations[t.ID] = task.ID
		if s.Executions == nil {
			s.Executions = map[string]*managedExecution{}
		}
		s.Executions[task.ID] = &managedExecution{Thread: t.ID, Pending: map[string]dg.Event{}}
		return t, nil
	})
	if err != nil {
		s.failBeforeStart(task, err)
		return
	}
	t := prepared.(Thread)
	if t.ServerID != "" {
		if err := s.Manager.Observe(dg.Event{TaskID: task.ID, Target: ThreadRef{Source: referenceSourceOf(task.From), Backend: task.Agent, SessionID: t.ServerID}.URL()}); err != nil {
			s.Release(task.ID)
			return
		}
	}
	_, err = s.OnMain(func() (any, error) {
		current := s.host.Thread(t.ID)
		if current.Busy || current.Loading || current.ReadOnly || s.host.TargetUnavailable(current) {
			return nil, errors.New("Target changed before submission")
		}
		text := task.Snapshot
		if text == "" {
			text = task.Prompt
		}
		id := s.host.Submit(SendRequest{ExecutionID: task.ID, Origin: "delegation", ThreadID: t.ID, Agent: task.Agent, Project: task.Workspace, Text: text, Model: task.Config.Model, Effort: task.Config.Effort, Policy: task.Config.Policy, ExplicitSettings: true, MemoryControl: true})
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
	if saveErr := s.Manager.Observe(dg.Event{TaskID: task.ID, State: dg.Failed, Result: dg.Result{Error: err.Error()}}); saveErr != nil {
		return
	}
	s.Release(task.ID)
}
func (s *collaborationState) Release(id string) {
	_, _ = s.OnMain(func() (any, error) {
		execution := s.Execution(id)
		if execution == nil {
			if task, err := s.Manager.Read(id); err == nil {
				if t, ok := s.host.ThreadByRef(task.Target); ok && s.Reservation(t.ID) == task.From+"\n"+task.RequestID {
					delete(s.Reservations, t.ID)
					s.host.DrainQueue(t.ID)
				}
			}
		}
		if execution != nil {
			if s.Reservation(execution.Thread) == id {
				delete(s.Reservations, execution.Thread)
			}
			s.host.Update(execution.Thread, func(t *Thread) {
				if t.ExecutionID == id {
					t.ExecutionID = ""
				}
			})
			delete(s.Executions, id)
			s.host.DrainQueue(execution.Thread)
		}
		return nil, nil
	})
}
func (s *collaborationState) Tick() {
	if s.LoadError != nil || s.ctx.Err() != nil {
		return
	}
	s.checkProgress()
	if err := s.Manager.ExpireDeliveries(); err != nil {
		return
	}
	if now := time.Now(); !now.Before(s.nextCollection) {
		if err := s.Manager.Collect(); err != nil {
			message := err.Error()
			s.cleanupError.Store(&message)
		} else {
			s.cleanupError.Store(nil)
		}
		s.nextCollection = now.Add(time.Hour)
	}
	tasks, err := s.Manager.ClaimQueued()
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
	return c.collaboration.BindExecution(ctx, id, target, epoch)
}

// bindExecution records the native session an execution is about to send
// its input to, before the input is sent.
func (s *collaborationState) BindExecution(ctx context.Context, id, target, epoch string) error {
	if id == "" || s == nil {
		return nil
	}
	if strings.HasPrefix(id, "delivery-") {
		if err := s.Manager.StorageError(); err != "" {
			return fmt.Errorf("STORAGE_UNAVAILABLE: %s", err)
		}
		d, ok := s.Manager.Delivery(id)
		if !ok || d.Notify != target || d.State != "submitting" {
			return errors.New("Notification target does not match its persisted receiver")
		}
		var allowed bool
		_, err := s.OnMain(func() (any, error) {
			t, found := s.host.ThreadByRef(target)
			allowed = found && !s.IsPaused(target) && s.Reservation(t.ID) == id && t.ExecutionID == id && !t.StopRequested
			return nil, nil
		})
		if err != nil {
			return err
		}
		if !allowed {
			return errManagedPaused
		}
		_, err = s.Call(func() (any, error) { return nil, s.Manager.BeginDeliveryInput(id) })
		if err != nil {
			return err
		}
		// Stop may have arrived while the input intent was syncing.
		_, err = s.OnMain(func() (any, error) {
			t, found := s.host.ThreadByRef(target)
			allowed = found && !s.IsPaused(target) && s.Reservation(t.ID) == id && t.ExecutionID == id && !t.StopRequested
			return nil, nil
		})
		if err == nil && !allowed {
			err = errManagedPaused
		}
		return err
	}
	done := make(chan error, 1)
	s.Post(func() {
		task, err := s.Manager.Read(id)
		if err == nil && task.CancelRequested {
			err = errManagedCancelled
		}
		if err == nil {
			err = s.Manager.Observe(dg.Event{TaskID: id, Target: target, SessionEpoch: epoch, SubmissionPhase: "submitting"})
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
	c.collaboration.Observe(id, event, saveOutcome)
}

// observe records what an execution's agent reported.
func (s *collaborationState) Observe(id string, event dg.Event, saveOutcome bool) {
	if id == "" || s == nil {
		return
	}
	if strings.HasPrefix(id, "delivery-") {
		outcome := "unknown"
		if event.State == dg.Running || event.State == dg.Completed {
			outcome = "accepted"
		}
		s.Post(func() {
			if d, ok := s.Manager.Delivery(id); ok && d.SubmissionPhase == "prepared" && outcome == "unknown" {
				outcome = "not_started"
			} else if ok && d.SubmissionPhase == "submitting" && event.NativeResultID != "" {
				// A root result (including a failed reply) proves the bound
				// Claude process accepted input; delivery is not reply success.
				outcome = "accepted"
			}
			if err := s.Manager.DeliveryOutcome(id, event.NativeTurnID, outcome, event.Result.Error); err == nil {
				s.Release(id)
			}
		})
		return
	}
	if run := s.Execution(id); run != nil {
		run.Sequence++
		event.Sequence = run.Sequence
	}
	event.TaskID = id
	s.Post(func() {
		if saveOutcome {
			if err := s.Manager.RecordNativeOutcome(event); err != nil {
				return
			}
		}
		if err := s.Manager.Observe(event); err != nil {
			return
		}
		if dg.Terminal(event.State) {
			s.Release(id)
		}
		s.Tick()
	})
}
