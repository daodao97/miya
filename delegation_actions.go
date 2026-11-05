package main

import (
	"miya/internal/model"
)

import dg "miya/internal/delegation"

// Native identity may still be pending. Persist cancellation first; startup
// acknowledgements also consult that intent before allowing execution to run.
func (s *collaborationState) CancelTask(caller, id string) (any, error) {
	task, err := s.Manager.Cancel(caller, id)
	if err != nil {
		return nil, err
	}
	err = s.InterruptTask(task)
	return taskPublic(task), err
}

// The caller has already committed the cancellation intent (possibly together
// with an idempotent operation receipt).
func (s *collaborationState) InterruptTask(task dg.Task) error {
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
		s.host.Update(t.ID, func(t *Thread) { t.StopRequested = true })
		if t.Busy && (model.ThreadAgent(t) != agentCodex || t.TurnID != "") {
			s.host.StopRun(t)
		}
		return nil, nil
	})
	return err
}

// pause holds deliveries to ref on the controller's thread and in the store.
// Each pause starts a generation, so a resume already in flight cannot undo it.
func (s *collaborationState) Pause(ref string) {
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

func (c *chatController) resumeCollaboration(ref string) deferred { return c.collaboration.Resume(ref) }

// Runs on the controller executor. A later Stop must win over an in-flight
// resume, even if it arrives between the durable write and this callback.
func (s *collaborationState) Resume(ref string) deferred {
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

func (s *collaborationState) RecheckTask(caller, id string) {
	work, err := s.OnMain(func() (any, error) { return s.host.PrepareRecheck(caller, id) })
	if err == nil {
		if run, ok := work.(deferred); ok {
			_, err = run()
		}
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	_, _ = s.Call(func() (any, error) { return nil, s.Manager.RecheckOutcome(id, failure) })
}
