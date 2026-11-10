package delegation

import "container/heap"

type oldestTasks []Task

func (h oldestTasks) Len() int { return len(h) }
func (h oldestTasks) Less(i, j int) bool {
	if h[i].FinishedAt.Equal(h[j].FinishedAt) {
		return h[i].ID < h[j].ID
	}
	return h[i].FinishedAt.Before(h[j].FinishedAt)
}
func (h oldestTasks) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *oldestTasks) Push(v any)   { *h = append(*h, v.(Task)) }
func (h *oldestTasks) Pop() any     { old := *h; v := old[len(old)-1]; *h = old[:len(old)-1]; return v }

// Collect commits expiration/pruning before removing files. Session ancestry
// outlives individual task records, so GC cannot reset depth or visibility.
func (m *Manager) Collect() error {
	err := m.change(func(s *Snapshot) error {
		protected := map[string]bool{}
		for _, w := range s.Watches {
			if w.State.Live() {
				protected[w.TaskID] = true
			}
		}
		for _, d := range s.Deliveries {
			if d.Resolution != ResolutionAbandoned && (d.State == NotifyPending || d.State.InFlight()) {
				for _, id := range d.TaskIDs {
					protected[id] = true
				}
			}
		}
		// An active task also protects its complete chain of predecessor evidence.
		var protect func(string)
		seen := map[string]bool{}
		protect = func(id string) {
			if id == "" || seen[id] {
				return
			}
			seen[id] = true
			protected[id] = true
			t := s.Tasks[id]
			protect(t.ParentTaskID)
			protect(t.PreviousTaskID)
		}
		for id, t := range s.Tasks {
			if !Terminal(t.State) || protected[id] {
				protect(id)
			}
		}
		refs := map[string]int{}
		terminalCount := 0
		for id, t := range s.Tasks {
			if t.ParentTaskID != "" {
				refs[t.ParentTaskID]++
			}
			if t.PreviousTaskID != "" {
				refs[t.PreviousTaskID]++
			}
			if !Terminal(t.State) {
				continue
			}
			terminalCount++
			if !protected[id] && !m.now().Before(t.FinishedAt.Add(m.retention)) && t.Result.Availability != ResultExpired {
				t.Prompt, t.Snapshot, t.Progress = "", "", ""
				t.Result = Result{Availability: ResultExpired}
				s.Tasks[id] = t
			}
		}
		eligible := func(id string) bool {
			t, ok := s.Tasks[id]
			return ok && Terminal(t.State) && !protected[id] && refs[id] == 0
		}
		candidates := &oldestTasks{}
		for id, t := range s.Tasks {
			if eligible(id) {
				heap.Push(candidates, t)
			}
		}
		removed := map[string]bool{}
		for terminalCount > m.maxTerminal && candidates.Len() > 0 {
			t := heap.Pop(candidates).(Task)
			if removed[t.ID] {
				continue
			}
			delete(s.Tasks, t.ID)
			removed[t.ID] = true
			terminalCount--
			for _, dependency := range []string{t.ParentTaskID, t.PreviousTaskID} {
				if dependency == "" {
					continue
				}
				refs[dependency]--
				if eligible(dependency) {
					heap.Push(candidates, s.Tasks[dependency])
				}
			}
		}
		for key, w := range s.Watches {
			if removed[w.TaskID] {
				delete(s.Watches, key)
			}
		}
		for key, op := range s.Operations {
			if removed[op.TaskID] {
				delete(s.Operations, key)
			}
			if op.Name == "cancel_all" && !m.now().Before(op.AcceptedAt.Add(m.retention)) {
				referenced := false
				for _, id := range op.TaskIDs {
					if _, ok := s.Tasks[id]; ok {
						referenced = true
						break
					}
				}
				if !referenced {
					delete(s.Operations, key)
				}
			}
		}
		for key, d := range s.Deliveries {
			var ids []string
			for _, id := range d.TaskIDs {
				if !removed[id] {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				delete(s.Deliveries, key)
			} else {
				d.TaskIDs = ids
				s.Deliveries[key] = d
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return m.store.Prune()
}
