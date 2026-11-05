package coordinator

import (
	"miya/internal/async"
	dg "miya/internal/delegation"
	"miya/internal/model"
)

// Native identity may still be pending. Persist cancellation first; startup
// acknowledgements also consult that intent before allowing execution to run.
func (s *Coordinator) CancelTask(caller, id string) (any, error) {
	task, err := s.Manager.Cancel(caller, id)
	if err != nil {
		return nil, err
	}
	err = s.InterruptTask(task)
	return TaskPublic(task), err
}

// The caller has already committed the cancellation intent (possibly together
// with an idempotent operation receipt).
func (s *Coordinator) InterruptTask(task dg.Task) error {
	if dg.Terminal(task.State) {
		return nil
	}
	id := task.ID
	_, err := s.OnMain(func() (any, error) {
		t, ok := s.host.ThreadByRef(task.Target)
		if !ok {
			if execution := s.Execution(id); execution != nil {
				t = s.host.Thread(execution.Thread)
				ok = t.ID != 0
			}
		}
		if !ok || t.ExecutionID != id {
			return nil, nil
		}
		s.host.Update(t.ID, func(t *model.Thread) { t.StopRequested = true })
		if t.Busy && (model.ThreadAgent(t) != model.AgentCodex || t.TurnID != "") {
			s.host.StopRun(t)
		}
		return nil, nil
	})
	return err
}

// pause holds deliveries to ref on the controller's thread and in the store.
// Each pause starts a generation, so a resume already in flight cannot undo it.
func (s *Coordinator) Pause(ref string) {
	if s.pausedRefs == nil {
		s.pausedRefs = map[string]bool{}
	}
	if s.pauseGen == nil {
		s.pauseGen = map[string]uint64{}
	}
	s.pausedRefs[ref] = true
	s.pauseGen[ref]++
	if s.Manager != nil {
		s.Post(func() { _ = s.Manager.Pause(ref, true) })
	}
}

// Runs on the controller executor. A later Stop must win over an in-flight
// resume, even if it arrives between the durable write and this callback.
func (s *Coordinator) Resume(ref string) async.Reply {
	generation := s.pauseGeneration(ref)
	return func() (any, error) {
		return s.Call(func() (any, error) {
			if err := s.Manager.Pause(ref, false); err != nil {
				return nil, err
			}
			return s.OnMain(func() (any, error) {
				if s.pauseGeneration(ref) == generation {
					delete(s.pausedRefs, ref)
				}
				return map[string]any{"paused": s.IsPaused(ref)}, nil
			})
		})
	}
}

func (s *Coordinator) RecheckTask(caller, id string) {
	work, err := s.OnMain(func() (any, error) { return s.host.PrepareRecheck(caller, id) })
	if err == nil {
		if run, ok := work.(async.Reply); ok {
			_, err = run()
		}
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	_, _ = s.Call(func() (any, error) { return nil, s.Manager.RecheckOutcome(id, failure) })
}
