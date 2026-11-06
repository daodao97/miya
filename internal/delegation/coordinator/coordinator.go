// Package coordinator runs the delegated tasks and completion notices of
// the background service: it starts each in a conversation of its Host,
// follows it, and records what happened in the delegation store.
package coordinator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/async"
	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/textutil"
)

var ErrCancelled = errors.New("Cancelled before native input was submitted")

var ErrPaused = errors.New("Collaboration delivery paused before native input was submitted")

type Execution struct {
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
type Coordinator struct {
	host           Host
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
	Executions   map[string]*Execution
	pausedRefs   map[string]bool
	pauseGen     map[string]uint64
}

// A window has no collaboration, so these read nothing on a nil state.

func (s *Coordinator) Reservation(thread int) string {
	if s == nil {
		return ""
	}
	return s.Reservations[thread]
}

func (s *Coordinator) Execution(id string) *Execution {
	if s == nil {
		return nil
	}
	return s.Executions[id]
}

func (s *Coordinator) IsPaused(ref string) bool {
	return s != nil && s.pausedRefs[ref]
}

// Context ends when the collaboration stops.
func (s *Coordinator) Context() context.Context { return s.ctx }

// Stop ends the collaboration's work; Done is closed once its worker
// returned.
func (s *Coordinator) Stop() { s.cancel() }

func (s *Coordinator) Done() <-chan struct{} { return s.done }

// Pending is how much collaboration work is queued or running.
func (s *Coordinator) Pending() int64 { return s.busy.Load() }

// CleanupError is why the last cleanup of finished tasks failed, "" when
// it did not.
func (s *Coordinator) CleanupError() string {
	if message := s.cleanupError.Load(); message != nil {
		return *message
	}
	return ""
}

// StartRecheck rechecks a task's native outcome in the background, as
// pending work.
func (s *Coordinator) StartRecheck(caller, id string) {
	s.busy.Add(1)
	go func() { defer s.busy.Add(-1); s.RecheckTask(caller, id) }()
}

func (s *Coordinator) pauseGeneration(ref string) uint64 {
	if s == nil {
		return 0
	}
	return s.pauseGen[ref]
}

// New opens the delegations stored in dir and works on them for host
// until parent ends.
func New(host Host, parent context.Context, dir string) *Coordinator {
	ctx, cancel := context.WithCancel(parent)
	s := &Coordinator{host: host, worker: async.NewExecutor(), ctx: ctx, cancel: cancel, done: make(chan struct{}), dispatched: map[string]bool{}}
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

func (s *Coordinator) Post(f func()) {
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

func (s *Coordinator) Call(f func() (any, error)) (any, error) {
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

func (s *Coordinator) OnMain(f func() (any, error)) (any, error) {
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

func (s *Coordinator) Create(in dg.StartInput) (dg.Task, error) {
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

func (s *Coordinator) start(task dg.Task) {
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
		t := model.Thread{}
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
			t = model.Thread{ID: s.host.NextID(), Agent: task.Agent, Title: title, Project: filepath.Base(task.Workspace), Loaded: true, HistoryLoaded: true, PermissionPolicy: task.Config.Policy}
			if task.Agent == model.AgentClaude {
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
			s.Executions = map[string]*Execution{}
		}
		s.Executions[task.ID] = &Execution{Thread: t.ID, Pending: map[string]dg.Event{}}
		return t, nil
	})
	if err != nil {
		s.failBeforeStart(task, err)
		return
	}
	t := prepared.(model.Thread)
	if t.ServerID != "" {
		if err := s.Manager.Observe(dg.Event{TaskID: task.ID, Target: model.ThreadRef{Source: referenceSourceOf(task.From), Backend: task.Agent, SessionID: t.ServerID}.URL()}); err != nil {
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
		id := s.host.Submit(model.SendRequest{ExecutionID: task.ID, Origin: "delegation", ThreadID: t.ID, Agent: task.Agent, Project: task.Workspace, Text: text, Model: task.Config.Model, Effort: task.Config.Effort, Policy: task.Config.Policy, ExplicitSettings: true, MemoryControl: true})
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

func (s *Coordinator) failBeforeStart(task dg.Task, err error) {
	if saveErr := s.Manager.Observe(dg.Event{TaskID: task.ID, State: dg.Failed, Result: dg.Result{Error: err.Error()}}); saveErr != nil {
		return
	}
	s.Release(task.ID)
}

func (s *Coordinator) Release(id string) {
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
			s.host.Update(execution.Thread, func(t *model.Thread) {
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

func (s *Coordinator) Tick() {
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

// bindExecution records the native session an execution is about to send
// its input to, before the input is sent.
func (s *Coordinator) BindExecution(ctx context.Context, id, target, epoch string) error {
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
			return ErrPaused
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
			err = ErrPaused
		}
		return err
	}
	done := make(chan error, 1)
	s.Post(func() {
		task, err := s.Manager.Read(id)
		if err == nil && task.CancelRequested {
			err = ErrCancelled
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

// observe records what an execution's agent reported.
func (s *Coordinator) Observe(id string, event dg.Event, saveOutcome bool) {
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
