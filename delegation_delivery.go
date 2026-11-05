package main

import (
	"errors"

	dg "miya/internal/delegation"
	"miya/internal/model"
)

// The controller checks eligibility twice, around the durable submission
// reservation. Only a verified pre-start refusal is retried.
func (s *collaborationState) deliverPending() {
	for _, notify := range s.manager.PendingReceivers() {
		value, err := s.onMain(func() (any, error) {
			t, ok := s.host.ThreadByRef(notify)
			return ok && !s.isPaused(notify) && !t.Busy && !t.Loading && !t.ReadOnly && !t.TaskPending && !s.host.TargetUnavailable(t) && len(t.Queue) == 0 && s.reservation(t.ID) == "" && !s.host.AwaitsUser(t) && s.host.Agent(model.ThreadAgent(t)).Ready, nil
		})
		if err != nil || !value.(bool) {
			continue
		}
		d, err := s.manager.ClaimDelivery(notify)
		if err != nil || d.ID == "" {
			continue
		}
		_, err = s.onMain(func() (any, error) {
			t, ok := s.host.ThreadByRef(notify)
			if !ok || s.isPaused(notify) || t.Busy || t.Loading || t.ReadOnly || t.TaskPending || s.host.TargetUnavailable(t) || len(t.Queue) > 0 || s.reservation(t.ID) != "" || s.host.AwaitsUser(t) || !s.host.Agent(model.ThreadAgent(t)).Ready {
				return nil, errors.New("Receiving conversation is not idle")
			}
			if s.reservations == nil {
				s.reservations = map[int]string{}
			}
			s.reservations[t.ID] = d.ID
			if s.executions == nil {
				s.executions = map[string]*managedExecution{}
			}
			s.executions[d.ID] = &managedExecution{thread: t.ID, pending: map[string]dg.Event{}}
			id := s.host.Submit(SendRequest{ExecutionID: d.ID, Origin: "delegation_notice", ThreadID: t.ID, Text: s.manager.NotificationText(d), Model: t.LaunchModel, Effort: t.LaunchEffort, Policy: s.host.Permission(t), ExplicitSettings: true, KeepSessionSettings: true, MemoryControl: true})
			if id == 0 {
				return nil, errors.New("Notification was rejected before native submission")
			}
			return nil, nil
		})
		if err != nil {
			if saveErr := s.manager.DeliveryOutcome(d.ID, "", "not_started", err.Error()); saveErr == nil {
				s.release(d.ID)
			}
		}
	}
}
