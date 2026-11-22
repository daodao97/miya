package coordinator

import (
	"errors"

	"miya/internal/chat/model"
	dg "miya/internal/delegation"
)

// The controller checks eligibility twice, around the durable submission
// reservation. Only a verified pre-start refusal is retried.
func (s *Coordinator) deliverPending() {
	for _, notify := range s.Manager.PendingReceivers() {
		value, err := s.OnMain(func() (any, error) {
			t, ok := s.host.ThreadByRef(notify)
			return ok && s.idleReceiver(t), nil
		})
		if err != nil || !value.(bool) {
			continue
		}
		d, err := s.Manager.ClaimDelivery(notify)
		if err != nil || d.ID == "" {
			continue
		}
		_, err = s.OnMain(func() (any, error) {
			t, ok := s.host.ThreadByRef(notify)
			if !ok || !s.allowsDelivery(d) || !s.idleReceiver(t) {
				return nil, errors.New("Receiving conversation is not idle")
			}
			if s.Reservations == nil {
				s.Reservations = map[int]string{}
			}
			s.Reservations[t.ID] = d.ID
			if s.Executions == nil {
				s.Executions = map[string]*Execution{}
			}
			s.Executions[d.ID] = &Execution{Thread: t.ID, Pending: map[string]dg.Event{}}
			origin := "delegation_notice"
			if d.IsFollowUp() {
				origin = "follow_up"
			}
			id := s.host.Submit(model.SendRequest{ExecutionID: d.ID, Origin: origin, ThreadID: t.ID, Text: s.Manager.NotificationText(d), Model: t.LaunchModel, Effort: t.LaunchEffort, Policy: s.host.Permission(t), ExplicitSettings: true, KeepSessionSettings: true, MemoryControl: true})
			if id == 0 {
				return nil, errors.New("Notification was rejected before native submission")
			}
			return nil, nil
		})
		if err != nil {
			if saveErr := s.Manager.DeliveryOutcome(d.ID, "", "not_started", err.Error()); saveErr == nil {
				s.Release(d.ID)
			}
		}
	}
}

func (s *Coordinator) idleReceiver(t model.Thread) bool {
	return !t.Busy && !t.Loading && !t.ReadOnly && !t.TaskPending && !s.host.TargetUnavailable(t) &&
		len(t.Queue) == 0 && s.Reservation(t.ID) == "" && !s.host.AwaitsUser(t) && s.host.Agent(model.ThreadAgent(t)).Ready
}

func (s *Coordinator) allowsDelivery(d dg.Delivery) bool {
	if d.IsFollowUp() {
		return s.stoppingFollowUps[d.Notify] == 0 && !s.cancellingFollowUp[d.ID]
	}
	return !s.IsPaused(d.Notify)
}
