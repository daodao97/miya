package main

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
		c := s.c
		t, ok := c.threadByReference(task.Target)
		if !ok {
			if execution := c.managedExecutions[id]; execution != nil {
				t = c.threadWithID(execution.thread)
				ok = t.ID != 0
			}
		}
		if !ok || t.ExecutionID != id {
			return nil, nil
		}
		c.update(t.ID, func(t *Thread) { t.StopRequested = true })
		if t.Busy && (threadAgent(t) != agentCodex || t.TurnID != "") {
			backendOf(t).Stop(c, t)
		}
		return nil, nil
	})
	return err
}

// Runs on the controller executor. A later Stop must win over an in-flight
// resume, even if it arrives between the durable write and this callback.
func (c *chatController) resumeCollaboration(ref string) deferred {
	s := c.collaboration
	generation := c.collaborationPauseGeneration[ref]
	return func() (any, error) {
		return s.call(func() (any, error) {
			if err := s.manager.Pause(ref, false); err != nil {
				return nil, err
			}
			return s.onMain(func() (any, error) {
				if c.collaborationPauseGeneration[ref] == generation {
					delete(c.collaborationPaused, ref)
				}
				return map[string]any{"paused": c.collaborationPaused[ref]}, nil
			})
		})
	}
}

func (s *collaborationState) recheckTask(caller, id string) {
	work, err := s.onMain(func() (any, error) { return s.c.prepareTaskRecheck(caller, id) })
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
