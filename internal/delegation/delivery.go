package delegation

import (
	"sort"
	"strings"
	"time"
)

const DeliveryTTL = 2 * time.Hour

func watchKey(task, notify string) string { return task + "\n" + notify }

func (m *Manager) enqueueNotifications(s *Snapshot, t Task) {
	for key, w := range s.Watches {
		if w.TaskID != t.ID || w.State != "watching" {
			continue
		}
		w.State = "pending"
		s.Watches[key] = w
		var id string
		for _, d := range s.Deliveries {
			if d.Notify == w.Notify && d.State == "pending" {
				id = d.ID
				break
			}
		}
		if id == "" {
			id = newID("delivery-")
			s.Deliveries[id] = Delivery{ID: id, Notify: w.Notify, State: "pending", CreatedAt: m.now(), ExpiresAt: t.FinishedAt.Add(DeliveryTTL)}
		}
		d := s.Deliveries[id]
		d.TaskIDs = append(d.TaskIDs, t.ID)
		sort.Strings(d.TaskIDs)
		if expiry := t.FinishedAt.Add(DeliveryTTL); expiry.Before(d.ExpiresAt) {
			d.ExpiresAt = expiry
		}
		s.Deliveries[id] = d
	}
}

func (m *Manager) Watch(caller, id string) (string, error) {
	var state string
	err := m.change(func(s *Snapshot) error {
		var err error
		state, err = m.watch(s, caller, id)
		return err
	})
	return state, err
}

func (m *Manager) watch(s *Snapshot, caller, id string) (string, error) {
	t, ok := s.Tasks[id]
	if !ok {
		return "", errCode("TASK_NOT_FOUND", "Unknown task")
	}
	if !canAccess(*s, caller, t) {
		return "", errCode("ACCESS_DENIED", "Task is not visible to this conversation")
	}
	if Terminal(t.State) {
		return "already_terminal", nil
	}
	if t.Resolution == "abandoned" {
		return "", errCode("INVALID_STATE", "Tracking this unresolved task was abandoned")
	}
	key := watchKey(id, caller)
	if w, ok := s.Watches[key]; ok && (w.State == "watching" || w.State == "pending" || w.State == "submitting" || w.State == "needsAttention") {
		return w.State, nil
	}
	s.Watches[key] = Watch{id, caller, "watching"}
	return "watching", nil
}

func (m *Manager) Unwatch(caller, id string) error {
	return m.change(func(s *Snapshot) error { return m.unwatch(s, caller, id) })
}

func (m *Manager) unwatch(s *Snapshot, caller, id string) error {
	t, ok := s.Tasks[id]
	if !ok {
		return errCode("TASK_NOT_FOUND", "Unknown task")
	}
	if !canAccess(*s, caller, t) {
		return errCode("ACCESS_DENIED", "Task is not visible")
	}
	key := watchKey(id, caller)
	w, ok := s.Watches[key]
	if !ok {
		return nil
	}
	if w.State == "submitting" || w.State == "delivered" || w.State == "needsAttention" {
		return errCode("ALREADY_SUBMITTED", "Notification has already started")
	}
	w.State = "cancelled"
	s.Watches[key] = w
	for did, d := range s.Deliveries {
		if d.Notify != caller || d.State != "pending" {
			continue
		}
		var tasks []string
		for _, taskID := range d.TaskIDs {
			if taskID != id {
				tasks = append(tasks, taskID)
			}
		}
		d.TaskIDs = tasks
		if len(tasks) == 0 {
			d.State = "cancelled"
		}
		s.Deliveries[did] = d
	}
	return nil
}

func (m *Manager) Pause(notify string, paused bool) error {
	if _, ok := referenceSource(notify); !ok {
		return errCode("INVALID_ARGUMENT", "Invalid receiving session")
	}
	return m.change(func(s *Snapshot) error { s.Paused[notify] = paused; return nil })
}

func (m *Manager) ClaimDelivery(notify string) (Delivery, error) {
	if err := m.ExpireDeliveries(); err != nil {
		return Delivery{}, err
	}
	view := m.view.Load()
	if view.Paused[notify] {
		return Delivery{}, nil
	}
	ready := false
	for _, d := range view.Deliveries {
		if d.Notify != notify || d.Resolution == "abandoned" {
			continue
		}
		if d.State == "submitting" || d.State == "needsAttention" {
			return Delivery{}, nil
		}
		ready = ready || d.State == "pending" && !m.now().Before(d.NextAttempt)
	}
	if !ready {
		return Delivery{}, nil
	}
	var claimed Delivery
	err := m.change(func(s *Snapshot) error {
		m.expireDeliveries(s)
		if s.Paused[notify] {
			return nil
		}
		for _, d := range s.Deliveries {
			if d.Notify == notify && d.Resolution != "abandoned" && (d.State == "submitting" || d.State == "needsAttention") {
				return nil
			}
		}
		var ids []string
		for id, d := range s.Deliveries {
			if d.Notify == notify && d.State == "pending" && d.Resolution == "" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			d := s.Deliveries[id]
			if !d.NextAttempt.IsZero() && m.now().Before(d.NextAttempt) {
				continue
			}
			d.State = "submitting"
			d.SubmissionPhase = "prepared"
			d.Attempts++
			d.NextAttempt = time.Time{}
			s.Deliveries[id] = d
			for _, taskID := range d.TaskIDs {
				w := s.Watches[watchKey(taskID, notify)]
				w.State = "submitting"
				s.Watches[watchKey(taskID, notify)] = w
			}
			claimed = d
			break
		}
		return nil
	})
	return claimed, err
}

// This intent must be durable before the adapter can write native input.
func (m *Manager) BeginDeliveryInput(id string) error {
	return m.change(func(s *Snapshot) error {
		d, ok := s.Deliveries[id]
		if !ok || d.State != "submitting" || d.Resolution != "" {
			return errCode("INVALID_STATE", "Notification is not reserved for input")
		}
		d.SubmissionPhase = "submitting"
		s.Deliveries[id] = d
		return nil
	})
}

// Abandon stops tracking this notification; its native outcome stays unknown.
// Later results may continue, but the old notification is never replayed.
func (m *Manager) abandonDelivery(s *Snapshot, op Operation) error {
	d, ok := s.Deliveries[op.DeliveryID]
	if !ok {
		return errCode("DELIVERY_NOT_FOUND", "Unknown notification")
	}
	if d.Notify != op.Caller {
		return errCode("ACCESS_DENIED", "Only the receiving conversation can resolve this notification")
	}
	matched := false
	for _, id := range d.TaskIDs {
		matched = matched || id == op.TaskID
	}
	if !matched || strings.TrimSpace(op.Reason) == "" {
		return errCode("INVALID_ARGUMENT", "A matching task and a resolution reason are required")
	}
	if d.State != "needsAttention" {
		return errCode("INVALID_STATE", "Only an uncertain notification can stop tracking")
	}
	d.Resolution = "abandoned"
	d.ResolutionReason, _ = bounded(op.Reason, 2000)
	s.Deliveries[d.ID] = d
	for _, id := range d.TaskIDs {
		key := watchKey(id, d.Notify)
		w := s.Watches[key]
		w.State = "cancelled"
		s.Watches[key] = w
	}
	return nil
}

// DeliveryOutcome distinguishes pre-start refusal from an ambiguous timeout.
// Only a proven pre-start refusal makes a notification eligible for retry.
func (m *Manager) DeliveryOutcome(id, turnID, outcome, reason string) error {
	return m.change(func(s *Snapshot) error {
		d, ok := s.Deliveries[id]
		if !ok {
			return errCode("DELIVERY_NOT_FOUND", "Unknown notification")
		}
		if d.State != "submitting" && d.State != "needsAttention" {
			return errCode("INVALID_STATE", "Notification is not awaiting a submission outcome")
		}
		switch outcome {
		case "accepted":
			d.State = "delivered"
			d.TargetTurnID = turnID
		case "not_started":
			if d.Resolution == "abandoned" {
				break
			}
			d.State = "pending"
			delays := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, time.Minute}
			attempt := d.Attempts - 1
			if attempt < 0 {
				attempt = 0
			}
			if attempt >= len(delays) {
				attempt = len(delays) - 1
			}
			d.NextAttempt = m.now().Add(delays[attempt])
		case "unknown":
			d.State = "needsAttention"
			d.SubmissionPhase = "submitting"
		default:
			return errCode("INVALID_ARGUMENT", "Unsupported delivery outcome")
		}
		d.Reason = reason
		s.Deliveries[id] = d
		for _, taskID := range d.TaskIDs {
			w := s.Watches[watchKey(taskID, d.Notify)]
			if d.Resolution != "abandoned" {
				w.State = d.State
			}
			s.Watches[watchKey(taskID, d.Notify)] = w
		}
		return nil
	})
}

// ExpireDeliveries advances deadlines even when receivers are offline or paused.
// An uncertain submission is never expired into something eligible for replay.
func (m *Manager) ExpireDeliveries() error {
	due := false
	for _, d := range m.view.Load().Deliveries {
		if d.State == "pending" && !m.now().Before(d.ExpiresAt) {
			due = true
			break
		}
	}
	if !due {
		return nil
	}
	return m.change(func(s *Snapshot) error { m.expireDeliveries(s); return nil })
}

func (m *Manager) expireDeliveries(s *Snapshot) {
	now := m.now()
	for id, d := range s.Deliveries {
		if d.State != "pending" {
			continue
		}
		var tasks []string
		var expiry time.Time
		for _, taskID := range d.TaskIDs {
			deadline := s.Tasks[taskID].FinishedAt.Add(DeliveryTTL)
			if !now.Before(deadline) {
				w := s.Watches[watchKey(taskID, d.Notify)]
				w.State = "expired"
				s.Watches[watchKey(taskID, d.Notify)] = w
				continue
			}
			tasks = append(tasks, taskID)
			if expiry.IsZero() || deadline.Before(expiry) {
				expiry = deadline
			}
		}
		d.TaskIDs = tasks
		d.ExpiresAt = expiry
		if len(tasks) == 0 {
			d.State = "expired"
			d.NextAttempt = time.Time{}
		}
		s.Deliveries[id] = d
	}
}
