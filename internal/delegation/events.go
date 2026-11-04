package delegation

import (
	"fmt"
	"strings"
)

func (m *Manager) Observe(e Event) error {
	return m.change(func(s *Snapshot) error {
		t, ok := s.Tasks[e.TaskID]
		if !ok {
			return errCode("TASK_NOT_FOUND", "Unknown execution")
		}
		if e.ExpectedRevision != 0 && t.Revision != e.ExpectedRevision {
			return errCode("STALE_EVENT", "Execution changed while native evidence was being read")
		}
		if e.Sequence != 0 && e.Sequence <= t.LastSequence {
			return nil
		}
		if t.Target != "" && e.Target != "" && t.Target != e.Target {
			return errCode("STALE_EVENT", "The event belongs to another session")
		}
		if t.NativeTurnID != "" && e.NativeTurnID != "" && t.NativeTurnID != e.NativeTurnID {
			return errCode("STALE_EVENT", "The event belongs to another turn")
		}
		if t.SessionEpoch != "" && e.SessionEpoch != "" && t.SessionEpoch != e.SessionEpoch {
			return errCode("STALE_EVENT", "The event belongs to another process generation")
		}
		// A late start response may add an identity but cannot reopen a terminal task.
		if t.Target == "" && e.Target != "" {
			source, ok := referenceSource(e.Target)
			from, _ := referenceSource(t.From)
			if !ok || source != from || e.Target == t.From || referenceAgent(e.Target) != t.Agent {
				return errCode("INVALID_ARGUMENT", "Invalid native session identity")
			}
			for _, other := range s.Tasks {
				if other.ID != t.ID && other.Target == e.Target && !Terminal(other.State) {
					return errCode("THREAD_BUSY", "Native identity belongs to another managed execution")
				}
			}
			t.Target = e.Target
			if t.NewSession {
				if parent, exists := s.Parents[t.Target]; exists && parent != t.From {
					return errCode("INVALID_ARGUMENT", "Session ancestry cannot change")
				}
				s.Parents[t.Target] = t.From
			}
		}
		if t.NativeTurnID == "" {
			t.NativeTurnID = e.NativeTurnID
		}
		if t.SessionEpoch == "" {
			t.SessionEpoch = e.SessionEpoch
		}
		if e.SubmissionPhase != "" {
			if e.SubmissionPhase != "submitting" {
				return errCode("INVALID_ARGUMENT", "Invalid native submission phase")
			}
			t.SubmissionPhase = e.SubmissionPhase
		}
		if e.Sequence != 0 {
			t.LastSequence = e.Sequence
		}
		if Terminal(t.State) {
			s.Tasks[t.ID] = t
			return nil
		}
		if t.State == Queued {
			return errCode("INVALID_STATE", "An unclaimed task cannot receive native events")
		}
		if e.State != "" && e.State != Running && e.State != NeedsAttention && !Terminal(e.State) {
			return errCode("INVALID_STATE", "Unsupported native execution state")
		}
		if e.State == Running {
			if t.Target == "" {
				return errCode("INVALID_STATE", "A running task requires a native session")
			}
			t.State = Running
			t.RecheckError = ""
			if t.StartedAt.IsZero() {
				t.StartedAt = m.now()
			}
		}
		if e.Progress != "" {
			t.Progress, _ = bounded(e.Progress, 4096)
		}
		t.WaitCause = e.WaitCause
		if e.State == NeedsAttention {
			t.State = NeedsAttention
			if e.Result.Error != "" {
				t.Result.Error, _ = bounded(e.Result.Error, 4096)
			}
		}
		if Terminal(e.State) {
			t.RecheckError = ""
			t.State = e.State
			t.WaitCause = ""
			t.FinishedAt = m.now()
			t.Result = boundedResult(e.Result)
		}
		s.Tasks[t.ID] = t
		if Terminal(t.State) && t.Resolution != "abandoned" {
			m.enqueueNotifications(s, t)
		}
		return nil
	})
}

func boundedResult(result Result) Result {
	text, truncated := bounded(strings.TrimSpace(result.Text), ResultLimit)
	result.Text, result.Truncated = text, result.Truncated || truncated
	result.Error, _ = bounded(result.Error, 4096)
	if result.Text != "" {
		result.Availability = "available"
		if result.Source == "" {
			result.Source = "final_message"
		}
	} else {
		result.Availability = "unavailable"
	}
	var files []string
	for _, file := range result.Artifacts {
		if len(files) == 100 {
			break
		}
		if file != "" {
			file, _ = bounded(file, 4096)
			files = append(files, file)
		}
	}
	result.Artifacts = files
	return result
}

// Progress cannot change execution identity or lifecycle. Checkpoint all
// active tasks in one index transaction rather than rewriting it per task.
func (m *Manager) ObserveProgress(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	return m.change(func(s *Snapshot) error {
		for _, e := range events {
			t, ok := s.Tasks[e.TaskID]
			if !ok || t.State != Running || e.State != Running || e.Target != t.Target || e.NativeTurnID != t.NativeTurnID || e.SessionEpoch != t.SessionEpoch || e.ExpectedRevision != t.Revision || e.Sequence <= t.LastSequence {
				continue
			}
			t.LastSequence = e.Sequence
			t.Progress, _ = bounded(e.Progress, 4096)
			t.WaitCause = e.WaitCause
			s.Tasks[t.ID] = t
		}
		return nil
	})
}

func (m *Manager) Cancel(caller, id string) (Task, error) {
	if err := m.change(func(s *Snapshot) error { return m.cancel(s, caller, id) }); err != nil {
		return Task{}, err
	}
	return m.Read(id)
}

func (m *Manager) cancel(s *Snapshot, caller, id string) error {
	t, ok := s.Tasks[id]
	if !ok {
		return errCode("TASK_NOT_FOUND", "Unknown task")
	}
	if caller != "" && caller != t.From {
		return errCode("ACCESS_DENIED", "Only the initiating conversation can cancel this task")
	}
	if Terminal(t.State) {
		return nil
	}
	t.CancelRequested = true
	if t.State == Queued {
		t.State = Interrupted
		t.FinishedAt = m.now()
		t.Result = Result{Availability: "unavailable", Error: "Cancelled before starting"}
	}
	s.Tasks[id] = t
	if Terminal(t.State) {
		m.enqueueNotifications(s, t)
	}
	return nil
}

func (m *Manager) Abandon(caller, id, reason string) error {
	return m.change(func(s *Snapshot) error { return m.abandon(s, caller, id, reason) })
}

func (m *Manager) abandon(s *Snapshot, caller, id, reason string) error {
	t, ok := s.Tasks[id]
	if !ok {
		return errCode("TASK_NOT_FOUND", "Unknown task")
	}
	if caller != "" && caller != t.From {
		return errCode("ACCESS_DENIED", "Only the initiating conversation can abandon this task")
	}
	if t.State != NeedsAttention {
		return errCode("INVALID_STATE", "Only an unresolved task can be abandoned")
	}
	if strings.TrimSpace(reason) == "" {
		return errCode("INVALID_ARGUMENT", "A reason is required")
	}
	t.Resolution = "abandoned"
	t.ResolutionReason, _ = bounded(strings.TrimSpace(reason), 2000)
	s.Tasks[id] = t
	for key, w := range s.Watches {
		if w.TaskID == id && w.State == "watching" {
			w.State = "cancelled"
			s.Watches[key] = w
		}
	}
	for key, d := range s.Deliveries {
		if d.State != "pending" {
			continue
		}
		var tasks []string
		for _, taskID := range d.TaskIDs {
			if taskID != id {
				tasks = append(tasks, taskID)
			} else {
				w := s.Watches[watchKey(id, d.Notify)]
				w.State = "cancelled"
				s.Watches[watchKey(id, d.Notify)] = w
			}
		}
		d.TaskIDs = tasks
		if len(tasks) == 0 {
			d.State = "cancelled"
		}
		s.Deliveries[key] = d
	}
	return nil
}

// Recover never assumes that a saved start was not accepted by the native
// process. Reservations survive restart until an adapter proves a terminal state.
func (m *Manager) Recover() error {
	return m.change(func(s *Snapshot) error {
		for id, t := range s.Tasks {
			if t.State == Starting && t.NativeTurnID == "" && t.StartedAt.IsZero() && (t.SubmissionPhase == "prepared" || t.NewSession && t.Agent == "codex" && t.Target == "") {
				// prepared is a durable promise made by this version: every
				// native input path commits submitting before writing input.
				// Legacy Claude records with an empty phase remain unknown.
				t.State = Failed
				t.FinishedAt = m.now()
				t.Result = Result{Availability: "unavailable", Error: "Service restarted before native input was submitted"}
				s.Tasks[id] = t
				if t.Resolution != "abandoned" {
					m.enqueueNotifications(s, t)
				}
			} else if Occupies(t.State) {
				t.State = NeedsAttention
				t.LastSequence = 0
				s.Tasks[id] = t
			}
		}
		for id, d := range s.Deliveries {
			if d.State == "submitting" {
				if d.SubmissionPhase == "prepared" {
					d.State = "pending"
					d.Reason = "Service restarted before notification input was submitted"
				} else {
					d.State = "needsAttention"
					d.Reason = "Native submission outcome must be verified after restart"
				}
				s.Deliveries[id] = d
				for _, taskID := range d.TaskIDs {
					key := watchKey(taskID, d.Notify)
					w := s.Watches[key]
					w.State = d.State
					s.Watches[key] = w
				}
			}
		}
		return nil
	})
}

func (m *Manager) NotificationText(d Delivery) string {
	s := m.view.Load()
	var b strings.Builder
	b.WriteString("[Miya collaboration] These delegated tasks finished. Read each fixed result with read_task, assess the findings, and continue the user's original task. Report issues, apply authorized fixes, and verify the outcome as needed; do not stop at acknowledging completion. Execution state alone does not establish correctness.\n")
	for _, id := range d.TaskIDs {
		t := s.Tasks[id]
		fmt.Fprintf(&b, "- %s: %s (task_id: %s, session: %s)\n", t.Title, t.State, id, t.Target)
	}
	return b.String()
}
