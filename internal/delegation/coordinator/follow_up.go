package coordinator

import (
	"errors"
	"time"

	"miya/internal/async"
	dg "miya/internal/delegation"
)

// ScheduleFollowUp starts with a verified tool call on the application
// executor. A Stop between verification and the write invalidates it.
func (s *Coordinator) ScheduleFollowUp(ref, call string, at time.Time, note string) func() (dg.Delivery, error) {
	generation := s.pauseGeneration(ref)
	return func() (dg.Delivery, error) {
		value, err := s.Call(func() (any, error) {
			_, err := s.OnMain(func() (any, error) {
				t, ok := s.host.ThreadByRef(ref)
				if !ok || t.StopRequested || t.ReadOnly || s.pauseGeneration(ref) != generation || s.stoppingFollowUps[ref] != 0 {
					return nil, errors.New("The conversation stopped before its follow-up was registered")
				}
				return nil, nil
			})
			if err != nil {
				return nil, err
			}
			return s.Manager.ScheduleFollowUp(ref, call, at, note)
		})
		if err != nil {
			return dg.Delivery{}, err
		}
		return value.(dg.Delivery), nil
	}
}

// StopFollowUps keeps the executor from starting a due timer while its
// cancellation is being written by the worker. Later turns can schedule anew.
func (s *Coordinator) StopFollowUps(ref string) {
	if s == nil || s.Manager == nil || ref == "" {
		return
	}
	if s.stoppingFollowUps == nil {
		s.stoppingFollowUps = map[string]uint64{}
	}
	if s.pauseGen == nil {
		s.pauseGen = map[string]uint64{}
	}
	s.pauseGen[ref]++
	generation := s.pauseGeneration(ref)
	s.stoppingFollowUps[ref] = generation
	s.Post(func() {
		if err := s.Manager.CancelFollowUps(ref, ""); err == nil {
			s.host.Dispatch(func() {
				if s.stoppingFollowUps[ref] == generation {
					delete(s.stoppingFollowUps, ref)
				}
			})
		}
	})
}

func (s *Coordinator) CancelFollowUp(d dg.Delivery) async.Reply {
	if s.cancellingFollowUp == nil {
		s.cancellingFollowUp = map[string]bool{}
	}
	s.cancellingFollowUp[d.ID] = true
	return func() (any, error) {
		_, err := s.Call(func() (any, error) {
			return nil, s.Manager.CancelFollowUps(d.Notify, d.ID)
		})
		s.host.Dispatch(func() { delete(s.cancellingFollowUp, d.ID) })
		if err != nil {
			return err.Error(), nil
		}
		return "", nil
	}
}
