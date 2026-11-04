// Package runtime maintains agent-independent, evidence-based session resources.
package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type SessionKey struct{ Source, Backend, SessionID string }
type Capabilities struct{ Tasks, Snapshot, NativePID, SessionRoot, SessionTag, RequiresHost bool }
type Task struct {
	ID, RunID, ToolID, Kind, Description, Status string
	RunOrder                                     string `json:",omitempty"`
	PID                                          int    `json:",omitempty"`
}
type Identity struct {
	Host, Boot, Start string
	PID               int
}
type Process struct {
	Identity                       Identity
	ParentPID                      int
	Name, TaskID, Evidence, Status string
	ObservedAt                     int64
}
type Root struct {
	PID      int
	TaskID   string
	Identity Identity
	Evidence string
}
type Endpoint struct {
	Process  Identity
	Host     string
	Port     int
	Protocol string
}
type View struct {
	Key          SessionKey
	Epoch        string
	Capabilities Capabilities
	Tasks        []Task
	TasksKnown   bool
	Processes    []Process
	Endpoints    []Endpoint
	ObservedAt   int64
	Notice       string `json:",omitempty"`
}
type record struct {
	View
	Bindings map[string]Identity
	Retired  map[string]bool
}
type Store struct {
	mu      sync.Mutex
	records map[SessionKey]*record
}

func New() *Store { return &Store{records: map[SessionKey]*record{}} }
func Active(status string) bool {
	return status == "running" || status == "pending" || status == "inProgress"
}
func (s *Store) Begin(key SessionKey, epoch string, caps Capabilities) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil {
		r = &record{View: View{Key: key}, Bindings: map[string]Identity{}, Retired: map[string]bool{}}
		s.records[key] = r
	}
	if r.Epoch != epoch {
		r.Epoch = epoch
		r.TasksKnown = false
		r.Bindings = map[string]Identity{}
		r.Retired = map[string]bool{}
		for i := range r.Tasks {
			if Active(r.Tasks[i].Status) {
				r.Tasks[i].Status = "unknown"
			}
		}
		for i := range r.Processes {
			if r.Processes[i].Status == "running" {
				r.Processes[i].Status = "unknown"
			}
		}
		r.Endpoints = nil
	}
	r.Capabilities = caps
}
func (s *Store) Tasks(key SessionKey, epoch string, tasks []Task, complete bool, notice string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil || r.Epoch != epoch {
		return false
	}
	r.Notice = notice
	if !complete {
		r.TasksKnown = false
		return true
	}
	old := map[string]Task{}
	for _, t := range r.Tasks {
		old[t.ID] = t
	}
	next := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if previous, ok := old[t.ID]; ok && previous.RunOrder != "" && t.RunOrder != "" && t.RunOrder < previous.RunOrder {
			t = previous
		}
		next = append(next, t)
	}
	present := map[string]bool{}
	for _, t := range next {
		tag := t.ID + "\x00" + t.RunID
		present[tag] = true
		delete(r.Retired, tag)
	}
	for _, t := range r.Tasks {
		tag := t.ID + "\x00" + t.RunID
		if !present[tag] {
			r.Retired[tag] = true
		}
	}
	r.Tasks = next
	r.TasksKnown = true
	return true
}

// PartialTask never resurrects a completed run and never pretends to be a full set.
func (s *Store) PartialTask(key SessionKey, epoch string, task Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil || r.Epoch != epoch {
		return
	}
	tag := task.ID + "\x00" + task.RunID
	if r.Retired[tag] && Active(task.Status) {
		return
	}
	if !Active(task.Status) && task.Status != "unknown" {
		r.Retired[tag] = true
	}
	for i, old := range r.Tasks {
		if old.ID != task.ID {
			continue
		}
		if old.RunOrder != "" && task.RunOrder != "" && task.RunOrder < old.RunOrder {
			return
		}
		if old.RunID == task.RunID && !Active(old.Status) && old.Status != "unknown" && Active(task.Status) {
			return
		}
		r.Tasks[i] = task
		return
	}
	r.Tasks = append(r.Tasks, task)
}

// Observe joins verified roots and previously observed identities to a complete
// OS snapshot. A PID reused by a different process cannot inherit ownership.
func (s *Store) Observe(key SessionKey, epoch string, all []Process, roots []Root, endpoints []Endpoint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil || r.Epoch != epoch {
		return false
	}
	byPID := map[int]Process{}
	for _, p := range all {
		byPID[p.Identity.PID] = p
	}
	owned := map[int]Process{}
	for _, old := range r.Processes {
		if p, ok := byPID[old.Identity.PID]; ok && p.Identity == old.Identity {
			p.TaskID, p.Evidence = old.TaskID, old.Evidence
			owned[p.Identity.PID] = p
		}
	}
	for _, root := range roots {
		p, ok := byPID[root.PID]
		if !ok || p.Identity.Start == "" || p.Identity.Boot == "" {
			continue
		}
		if root.Identity != (Identity{}) && root.Identity != p.Identity {
			continue
		}
		if bound, ok := r.Bindings[root.TaskID]; ok && bound != p.Identity {
			continue
		}
		r.Bindings[root.TaskID] = p.Identity
		p.TaskID, p.Evidence = root.TaskID, "native"
		if root.Evidence != "" {
			p.Evidence = root.Evidence
		}
		owned[root.PID] = p
	}
	for changed := true; changed; {
		changed = false
		for _, p := range all {
			if _, ok := owned[p.Identity.PID]; ok {
				continue
			}
			if parent, ok := owned[p.ParentPID]; ok && parent.Identity != p.Identity {
				p.TaskID, p.Evidence = parent.TaskID, "descendant"
				owned[p.Identity.PID] = p
				changed = true
			}
		}
	}
	now := time.Now().UnixMilli()
	processes := make([]Process, 0, len(owned))
	for _, p := range owned {
		p.Status = "running"
		p.ObservedAt = now
		processes = append(processes, p)
	}
	for _, old := range r.Processes {
		if p, ok := owned[old.Identity.PID]; !ok || p.Identity != old.Identity {
			old.Status = "exited"
			old.ObservedAt = now
			processes = append(processes, old)
		}
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].Identity.PID < processes[j].Identity.PID })
	// Retain a bounded tail of exited processes for context, never as live roots.
	live := processes[:0]
	exited := []Process{}
	for _, p := range processes {
		if p.Status == "running" {
			live = append(live, p)
		} else {
			exited = append(exited, p)
		}
	}
	if len(exited) > 64 {
		exited = exited[len(exited)-64:]
	}
	r.Processes = append(live, exited...)
	r.Endpoints = nil
	for _, e := range endpoints {
		if p, ok := owned[e.Process.PID]; ok && p.Identity == e.Process {
			r.Endpoints = append(r.Endpoints, e)
		}
	}
	sort.Slice(r.Endpoints, func(i, j int) bool {
		if r.Endpoints[i].Port == r.Endpoints[j].Port {
			return r.Endpoints[i].Host < r.Endpoints[j].Host
		}
		return r.Endpoints[i].Port < r.Endpoints[j].Port
	})
	r.ObservedAt = now
	return true
}
func (s *Store) Unknown(key SessionKey, epoch, notice string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil || r.Epoch != epoch {
		return
	}
	r.Notice = notice
	r.Endpoints = nil
	for i := range r.Processes {
		if r.Processes[i].Status == "running" {
			r.Processes[i].Status = "unknown"
		}
	}
}
func (s *Store) Read(key SessionKey) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil {
		return View{Key: key}
	}
	v := r.View
	v.Tasks = append([]Task{}, v.Tasks...)
	v.Processes = append([]Process{}, v.Processes...)
	v.Endpoints = append([]Endpoint{}, v.Endpoints...)
	return v
}
func (s *Store) Save(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]record, 0, len(s.records))
	for _, r := range s.records {
		v := *r
		v.Tasks = append([]Task(nil), r.Tasks...)
		for i := range v.Tasks {
			v.Tasks[i].Description = Summary(v.Tasks[i].Description)
		}
		records = append(records, v)
	}
	data, err := json.Marshal(struct {
		Version int
		Records []record
	}{1, records})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".runtime-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func Load(path string) *Store {
	s := New()
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var saved struct {
		Version int
		Records []record
	}
	if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
		return s
	}
	for _, r := range saved.Records {
		r.TasksKnown = false
		r.Endpoints = nil
		r.Notice = "Waiting for verification"
		for i := range r.Tasks {
			r.Tasks[i].Status = "unknown"
		}
		for i := range r.Processes {
			if r.Processes[i].Status == "running" {
				r.Processes[i].Status = "unknown"
			}
		}
		if r.Retired == nil {
			r.Retired = map[string]bool{}
		}
		if r.Bindings == nil {
			r.Bindings = map[string]Identity{}
		}
		s.records[r.Key] = &r
	}
	return s
}

func (s *Store) TasksUnknown(key SessionKey, epoch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil || r.Epoch != epoch {
		return
	}
	r.TasksKnown = false
	for i := range r.Tasks {
		if Active(r.Tasks[i].Status) {
			r.Tasks[i].Status = "unknown"
		}
	}
}
