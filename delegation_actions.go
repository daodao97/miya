package main

import (
	"miya/internal/model"
)

import dg "miya/internal/delegation"

// Native identity may still be pending. Persist cancellation first; startup
// acknowledgements also consult that intent before allowing execution to run.
func (s *collaborationState) cancelTask(caller, id string) (any, error) {
	task, err := s.manager.Cancel(caller, id)
	if err != nil {
		return nil, err
	}
	err = s.interruptTask(task)
	return taskPublic(task), err
}

// The caller has already committed the cancellation intent (possibly together
// with an idempotent operation receipt).
func (s *collaborationState) interruptTask(task dg.Task) error {
	if dg.Terminal(task.State) {
		return nil
	}
	id := task.ID
	_, err := s.onMain(func() (any, error) {
		t, ok := s.host.ThreadByRef(task.Target)
		if !ok {
			if execution := s.execution(id); execution != nil {
				t = s.host.Thread(execution.thread)
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
func (s *collaborationState) pause(ref string) {
	if s.pausedRefs == nil {
		s.pausedRefs = map[string]bool{}
	}
	if s.pauseGen == nil {
		s.pauseGen = map[string]uint64{}
	}
	s.pausedRefs[ref] = true
	s.pauseGen[ref]++
	if s.manager != nil {
		s.post(func() { _ = s.manager.Pause(ref, true) })
	}
}

func (c *chatController) resumeCollaboration(ref string) deferred { return c.collaboration.resume(ref) }

// Runs on the controller executor. A later Stop must win over an in-flight
// resume, even if it arrives between the durable write and this callback.
func (s *collaborationState) resume(ref string) deferred {
	generation := s.pauseGeneration(ref)
	return func() (any, error) {
		return s.call(func() (any, error) {
			if err := s.manager.Pause(ref, false); err != nil {
				return nil, err
			}
			return s.onMain(func() (any, error) {
				if s.pauseGeneration(ref) == generation {
					delete(s.pausedRefs, ref)
				}
				return map[string]any{"paused": s.isPaused(ref)}, nil
			})
		})
	}
}

func (s *collaborationState) recheckTask(caller, id string) {
	work, err := s.onMain(func() (any, error) { return s.host.PrepareRecheck(caller, id) })
	if err == nil {
		if run, ok := work.(deferred); ok {
			_, err = run()
		}
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	_, _ = s.call(func() (any, error) { return nil, s.manager.RecheckOutcome(id, failure) })
}
