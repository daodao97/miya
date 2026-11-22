package delegation

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxFollowUps     = 3
	MaxFollowUpNote  = 400
	MaxFollowUpDelay = 30 * 24 * time.Hour
)

func (m *Manager) ScheduleFollowUp(ref, call string, at time.Time, note string) (Delivery, error) {
	if _, ok := referenceSource(ref); !ok || call == "" {
		return Delivery{}, errCode("CALLER_UNAVAILABLE", "A verified conversation and tool call are required")
	}
	note = strings.Join(strings.Fields(note), " ")
	if note == "" || utf8.RuneCountInString(note) > MaxFollowUpNote {
		return Delivery{}, errCode("INVALID_ARGUMENT", "Supply a follow-up note of 1 to 400 characters")
	}
	id := "delivery-" + digest([]byte("follow_up\n" + ref + "\n" + call))[:32]
	at = at.UTC()
	var result Delivery
	err := m.change(func(s *Snapshot) error {
		if existing, ok := s.Deliveries[id]; ok {
			if !existing.IsFollowUp() || existing.Notify != ref || !existing.FollowUpAt.Equal(at) || existing.FollowUpNote != note {
				return errCode("INVALID_ARGUMENT", "This tool call already scheduled a different follow-up")
			}
			result = existing
			return nil
		}
		now := m.now()
		if !at.After(now) || at.After(now.Add(MaxFollowUpDelay)) {
			return errCode("INVALID_ARGUMENT", "Choose a follow-up time in the next 30 days")
		}
		count := 0
		for _, d := range s.Deliveries {
			if d.Notify == ref && d.IsFollowUp() && d.State.Live() && d.Resolution == "" {
				count++
			}
		}
		if count >= MaxFollowUps {
			return errCode("LIMIT_REACHED", "A conversation may have at most three outstanding follow-ups")
		}
		result = Delivery{ID: id, Notify: ref, State: NotifyPending, CreatedAt: now, FollowUpAt: at, FollowUpNote: note}
		s.Deliveries[id] = result
		return nil
	})
	return result, err
}

// FollowUps gives windows and phones the current timers and ambiguous inputs.
func (m *Manager) FollowUps() []Delivery {
	var out []Delivery
	for _, d := range m.view.Load().Deliveries {
		if d.IsFollowUp() && d.State.Live() && d.Resolution == "" {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Delivery) int {
		if order := a.FollowUpAt.Compare(b.FollowUpAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (m *Manager) HasPendingFollowUps() bool {
	for _, d := range m.view.Load().Deliveries {
		if d.IsFollowUp() && d.State == NotifyPending && d.Resolution == "" {
			return true
		}
	}
	return false
}

// CancelFollowUps cancels an individual timer, or all timers for this receiver
// when id is empty. A timer which might already have sent input is never replayed.
func (m *Manager) CancelFollowUps(ref, id string) error {
	return m.change(func(s *Snapshot) error {
		if id != "" {
			d, exists := s.Deliveries[id]
			if !exists || !d.IsFollowUp() || d.Notify != ref {
				return errCode("FOLLOW_UP_NOT_FOUND", "The follow-up is no longer available")
			}
		}
		for key, d := range s.Deliveries {
			if d.Notify != ref || !d.IsFollowUp() || id != "" && key != id || !d.State.Live() {
				continue
			}
			if d.State.InFlight() {
				d.Resolution = ResolutionAbandoned
				d.ResolutionReason = "The user stopped tracking this follow-up"
			} else {
				d.State = NotifyCancelled
				d.NextAttempt = time.Time{}
			}
			s.Deliveries[key] = d
		}
		return nil
	})
}

func deliveryDue(d Delivery, now time.Time, paused bool) bool {
	return d.State == NotifyPending && d.Resolution == "" && !now.Before(d.NextAttempt) &&
		(d.IsFollowUp() && !now.Before(d.FollowUpAt) || !d.IsFollowUp() && !paused)
}
