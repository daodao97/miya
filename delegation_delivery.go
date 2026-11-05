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
			c := s.c
			t, ok := c.threadByReference(notify)
			return ok && !c.collaborationPaused[notify] && !t.Busy && !t.Loading && !t.ReadOnly && !t.TaskPending && !c.managedTargetUnavailable(t) && len(t.Queue) == 0 && c.managedReservations[t.ID] == "" && !c.threadAwaitsUser(t) && c.agentStatus(model.ThreadAgent(t)).Ready, nil
		})
		if err != nil || !value.(bool) {
			continue
		}
		d, err := s.manager.ClaimDelivery(notify)
		if err != nil || d.ID == "" {
			continue
		}
		_, err = s.onMain(func() (any, error) {
			c := s.c
			t, ok := c.threadByReference(notify)
			if !ok || c.collaborationPaused[notify] || t.Busy || t.Loading || t.ReadOnly || t.TaskPending || c.managedTargetUnavailable(t) || len(t.Queue) > 0 || c.managedReservations[t.ID] != "" || c.threadAwaitsUser(t) || !c.agentStatus(model.ThreadAgent(t)).Ready {
				return nil, errors.New("Receiving conversation is not idle")
			}
			if c.managedReservations == nil {
				c.managedReservations = map[int]string{}
			}
			c.managedReservations[t.ID] = d.ID
			if c.managedExecutions == nil {
				c.managedExecutions = map[string]*managedExecution{}
			}
			c.managedExecutions[d.ID] = &managedExecution{thread: t.ID, pending: map[string]dg.Event{}}
			id := c.submitExpanded(SendRequest{ExecutionID: d.ID, Origin: "delegation_notice", ThreadID: t.ID, Text: s.manager.NotificationText(d), Model: t.LaunchModel, Effort: t.LaunchEffort, Policy: c.permissionForThread(t), ExplicitSettings: true, KeepSessionSettings: true, MemoryControl: true})
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
