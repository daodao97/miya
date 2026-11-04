package delegation

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Options struct {
	Limit       int
	Now         func() time.Time
	Retention   time.Duration
	MaxTerminal int
}

// Manager serializes durable changes off the application executor. Published
// views are immutable and contain only successfully committed revisions.
type Manager struct {
	mu           sync.Mutex
	store        *Store
	state        Snapshot
	view         atomic.Pointer[Snapshot]
	writeError   error
	storageError atomic.Pointer[string]
	limit        int
	now          func() time.Time
	retention    time.Duration
	maxTerminal  int
}

func Open(store *Store, options Options) (*Manager, error) {
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	if options.Limit <= 0 {
		options.Limit = 4
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Retention <= 0 {
		options.Retention = 30 * 24 * time.Hour
	}
	if options.MaxTerminal <= 0 {
		options.MaxTerminal = 10000
	}
	m := &Manager{store: store, state: state, limit: options.Limit, now: options.Now, retention: options.Retention, maxTerminal: options.MaxTerminal}
	if err := validateSnapshot(state); err != nil {
		return nil, err
	}
	m.publish()
	return m, nil
}

func (m *Manager) publish() {
	v := m.state
	v.queued, v.occupied, v.active = 0, 0, 0
	for _, t := range v.Tasks {
		if t.State == Queued {
			v.queued++
		}
		if Occupies(t.State) {
			v.occupied++
		}
		if t.State == Running || t.State == Starting {
			v.active++
		}
	}
	m.view.Store(&v)
}

func (m *Manager) HasActiveExecutions() bool { return m.view.Load().active != 0 }
func (m *Manager) Paused(ref string) bool    { return m.view.Load().Paused[ref] }

// Only pending receivers are needed by the scheduler. Do not clone all
// historical task bodies and operation receipts on every idle tick.
func (m *Manager) PendingReceivers() []string {
	seen := map[string]bool{}
	var refs []string
	for _, d := range m.view.Load().Deliveries {
		if d.State == "pending" && d.Resolution == "" && !seen[d.Notify] {
			refs = append(refs, d.Notify)
			seen[d.Notify] = true
		}
	}
	return refs
}

func (m *Manager) Delivery(id string) (Delivery, bool) {
	d, ok := m.view.Load().Deliveries[id]
	d.TaskIDs = slices.Clone(d.TaskIDs)
	return d, ok
}

// Notification reads only watch/delivery metadata. Frequent task waits must
// not clone historical task bodies or operation receipts.
func (m *Manager) Notification(id, from string) (Watch, Delivery, bool) {
	s := m.view.Load()
	w := s.Watches[watchKey(id, from)]
	for _, d := range s.Deliveries {
		if d.Notify != from {
			continue
		}
		for _, taskID := range d.TaskIDs {
			if taskID == id {
				d.TaskIDs = slices.Clone(d.TaskIDs)
				return w, d, s.Paused[from]
			}
		}
	}
	return w, Delivery{}, s.Paused[from]
}
func (m *Manager) View() Snapshot   { return cloneSnapshot(*m.view.Load()) }
func (m *Manager) Revision() uint64 { return m.view.Load().Revision }
func cloneSnapshot(s Snapshot) Snapshot {
	out := s
	out.Tasks = maps.Clone(s.Tasks)
	out.Parents = maps.Clone(s.Parents)
	out.Watches = maps.Clone(s.Watches)
	out.Deliveries = maps.Clone(s.Deliveries)
	out.Paused = maps.Clone(s.Paused)
	out.Operations = maps.Clone(s.Operations)
	for key, op := range out.Operations {
		op.TaskIDs = slices.Clone(op.TaskIDs)
		out.Operations[key] = op
	}
	for id, t := range out.Tasks {
		t.Result.Artifacts = slices.Clone(t.Result.Artifacts)
		out.Tasks[id] = t
	}
	for id, d := range out.Deliveries {
		d.TaskIDs = slices.Clone(d.TaskIDs)
		out.Deliveries[id] = d
	}
	return out
}
func (m *Manager) Read(id string) (Task, error) {
	t, ok := m.view.Load().Tasks[id]
	if !ok {
		return Task{}, errCode("TASK_NOT_FOUND", "The task no longer exists")
	}
	t.Result.Artifacts = slices.Clone(t.Result.Artifacts)
	return t, nil
}

func (m *Manager) FindRequest(from, requestID string) (Task, bool) {
	for _, t := range m.view.Load().Tasks {
		if t.From == from && t.RequestID == requestID {
			t.Result.Artifacts = slices.Clone(t.Result.Artifacts)
			return t, true
		}
	}
	return Task{}, false
}

// Health is read without acquiring the writer lock, which may be waiting for IO.
func (m *Manager) StorageError() string {
	if e := m.storageError.Load(); e != nil {
		return *e
	}
	return ""
}

func (m *Manager) change(fn func(*Snapshot) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeError != nil {
		return errCode("STORAGE_UNAVAILABLE", m.writeError.Error())
	}
	next := cloneSnapshot(m.state)
	if err := fn(&next); err != nil {
		return err
	}
	if reflect.DeepEqual(next, m.state) {
		return nil
	}
	for id, t := range next.Tasks {
		old, exists := m.state.Tasks[id]
		if !exists || !reflect.DeepEqual(t, old) {
			t.Revision++
			next.Tasks[id] = t
		}
	}
	next.Revision++
	if err := validateSnapshot(next); err != nil {
		return err
	}
	if err := m.store.Save(next); err != nil {
		m.writeError = err
		message := err.Error()
		m.storageError.Store(&message)
		return errCode("STORAGE_UNAVAILABLE", err.Error())
	}
	m.state = next
	m.publish()
	return nil
}

var sourcePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var backendPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func referenceSource(ref string) (string, bool) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "miya" || !sourcePattern.MatchString(u.Host) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	p := strings.Split(u.EscapedPath(), "/")
	if len(p) != 4 || p[0] != "" || p[1] != "thread" || !backendPattern.MatchString(p[2]) {
		return "", false
	}
	id, err := url.PathUnescape(p[3])
	if err != nil || id == "" || strings.ContainsAny(id, "\x00\r\n") || url.PathEscape(id) != p[3] {
		return "", false
	}
	return u.Host, true
}

func referenceAgent(ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	parts := strings.Split(u.EscapedPath(), "/")
	if len(parts) != 4 {
		return ""
	}
	return parts[2]
}

func validState(s State) bool { return s == Queued || Occupies(s) || Terminal(s) }
func validateSnapshot(s Snapshot) error {
	requests := map[string]bool{}
	for id, t := range s.Tasks {
		if !taskIDPattern.MatchString(id) || t.ID != id || !validState(t.State) {
			return fmt.Errorf("invalid delegation task %q", id)
		}
		if t.Revision == 0 && s.Revision != 0 {
			return fmt.Errorf("invalid task revision")
		}
		if !hashPattern.MatchString(t.RequestHash) || !backendPattern.MatchString(t.Agent) || !filepath.IsAbs(t.Workspace) || t.Depth < 1 || t.Depth > 2 || t.CreatedAt.IsZero() {
			return fmt.Errorf("invalid task metadata")
		}
		if t.Resolution != "" && t.Resolution != "abandoned" {
			return fmt.Errorf("invalid task resolution")
		}
		if t.SubmissionPhase != "" && t.SubmissionPhase != "prepared" && t.SubmissionPhase != "submitting" {
			return fmt.Errorf("invalid task submission phase")
		}
		if Terminal(t.State) {
			if t.FinishedAt.IsZero() || (t.Result.Availability != "available" && t.Result.Availability != "unavailable" && t.Result.Availability != "expired") {
				return fmt.Errorf("invalid terminal result")
			}
		} else if !t.FinishedAt.IsZero() || t.Result.Availability != "pending" {
			return fmt.Errorf("invalid active task result")
		}
		source, ok := referenceSource(t.From)
		if !ok {
			return fmt.Errorf("invalid delegation caller")
		}
		if t.Target != "" {
			target, ok := referenceSource(t.Target)
			if !ok || target != source || referenceAgent(t.Target) != t.Agent {
				return fmt.Errorf("invalid delegation target")
			}
		}
		key := t.From + "\n" + t.RequestID
		if t.RequestID == "" || requests[key] {
			return fmt.Errorf("invalid delegation request identity")
		}
		requests[key] = true
		if t.ParentTaskID != "" {
			if parent, ok := s.Tasks[t.ParentTaskID]; !ok || parent.Target != t.From || parent.Depth >= t.Depth {
				return fmt.Errorf("missing task ancestor")
			}
		}
		if t.PreviousTaskID != "" {
			previous, ok := s.Tasks[t.PreviousTaskID]
			if !ok || previous.ID == t.ID || previous.Target != t.Target || !Terminal(previous.State) {
				return fmt.Errorf("invalid previous task")
			}
		}
		seen := map[string]bool{id: true}
		for parent := t.ParentTaskID; parent != ""; parent = s.Tasks[parent].ParentTaskID {
			if seen[parent] {
				return fmt.Errorf("cyclic task ancestry")
			}
			seen[parent] = true
			if _, ok := s.Tasks[parent]; !ok {
				return fmt.Errorf("missing task ancestor")
			}
		}
	}
	for child, parent := range s.Parents {
		source, ok := referenceSource(child)
		p, valid := referenceSource(parent)
		if !ok || !valid || source != p || child == parent {
			return fmt.Errorf("invalid session ancestry")
		}
		if _, err := sessionDepth(s, child); err != nil {
			return err
		}
	}
	validNotificationState := func(state string) bool {
		return state == "pending" || state == "submitting" || state == "delivered" || state == "cancelled" || state == "expired" || state == "needsAttention"
	}
	for key, w := range s.Watches {
		t, ok := s.Tasks[w.TaskID]
		if !ok || key != watchKey(w.TaskID, w.Notify) || !canAccess(s, w.Notify, t) || (w.State != "watching" && !validNotificationState(w.State)) {
			return fmt.Errorf("invalid task watch")
		}
	}
	for id, d := range s.Deliveries {
		if d.SubmissionPhase != "" && d.SubmissionPhase != "prepared" && d.SubmissionPhase != "submitting" || d.Resolution != "" && d.Resolution != "abandoned" {
			return fmt.Errorf("invalid delivery submission phase or resolution")
		}
		if id != d.ID || !deliveryIDPattern.MatchString(id) || !validNotificationState(d.State) || d.CreatedAt.IsZero() || d.Attempts < 0 {
			return fmt.Errorf("invalid delivery identity")
		}
		seen := map[string]bool{}
		for _, taskID := range d.TaskIDs {
			t, ok := s.Tasks[taskID]
			if !ok || !Terminal(t.State) || seen[taskID] || !canAccess(s, d.Notify, t) {
				return fmt.Errorf("invalid delivery task")
			}
			seen[taskID] = true
		}
	}
	for key, receipt := range s.Operations {
		data, _ := json.Marshal(receipt.Operation)
		if key != receipt.Caller+"\n"+receipt.RequestID || !receipt.Accepted || receipt.Hash != digest(data) {
			return fmt.Errorf("invalid operation receipt")
		}
		if _, ok := referenceSource(receipt.Caller); !ok || receipt.RequestID == "" || receipt.AcceptedAt.IsZero() {
			return fmt.Errorf("invalid operation identity")
		}
		if _, ok := s.Tasks[receipt.TaskID]; (receipt.Name != "cancel_all" && !ok) || requests[key] {
			return fmt.Errorf("invalid operation task or conflicting request")
		}
		switch receipt.Name {
		case "cancel_task", "watch_task", "unwatch_task", "abandon", "recheck", "abandon_delivery":
			if len(receipt.TaskIDs) != 0 {
				return fmt.Errorf("invalid single-task receipt scope")
			}
			if receipt.Name == "abandon_delivery" && !deliveryIDPattern.MatchString(receipt.DeliveryID) || receipt.Name != "abandon_delivery" && receipt.DeliveryID != "" {
				return fmt.Errorf("invalid delivery receipt identity")
			}
		case "cancel_all":
			if receipt.TaskID != "" {
				return fmt.Errorf("invalid batch receipt scope")
			}
			seen := map[string]bool{}
			for _, id := range receipt.TaskIDs {
				if !taskIDPattern.MatchString(id) || seen[id] {
					return fmt.Errorf("invalid batch receipt task")
				}
				seen[id] = true
			}
		default:
			return fmt.Errorf("invalid receipt operation")
		}
	}
	return nil
}

func sessionDepth(s Snapshot, ref string) (int, error) {
	seen := map[string]bool{}
	depth := 0
	for ref != "" {
		if seen[ref] {
			return 0, fmt.Errorf("cyclic session ancestry")
		}
		seen[ref] = true
		ref = s.Parents[ref]
		if ref != "" {
			depth++
		}
	}
	return depth, nil
}

func ancestor(s Snapshot, parent, child string) bool {
	seen := map[string]bool{}
	for child != "" && !seen[child] {
		if child == parent {
			return true
		}
		seen[child] = true
		child = s.Parents[child]
	}
	return false
}
func canAccess(s Snapshot, caller string, t Task) bool {
	return caller != "" && (ancestor(s, caller, t.From) || t.Target != "" && ancestor(s, caller, t.Target))
}
func (m *Manager) CanAccess(caller, id string) bool {
	s := m.view.Load()
	t, ok := s.Tasks[id]
	return ok && canAccess(*s, caller, t)
}
func occupied(s Snapshot) int {
	n := 0
	for _, t := range s.Tasks {
		if Occupies(t.State) {
			n++
		}
	}
	return n
}
func requestHash(in StartInput) string {
	in.Effective = Config{}
	in.Snapshot = ""
	in.ParentTaskID = ""
	data, _ := json.Marshal(in)
	return digest(data)
}

// RequestNotifies resolves an omitted default against the original request
// hash, not against the current watch (which may since have been cancelled).
func (m *Manager) RequestNotifies(in StartInput) bool {
	t, ok := m.FindRequest(in.From, in.RequestID)
	in.Notify = true
	if in.Context == "" {
		in.Context = "none"
	}
	return ok && t.RequestHash == requestHash(in)
}

func (m *Manager) Create(in StartInput) (Task, error) {
	if in.Context == "" {
		in.Context = "none"
	}
	// An already committed request is a read, including while writes are
	// disabled. Do not make an accepted task disappear behind a later IO fault.
	if previous, ok := m.FindRequest(in.From, in.RequestID); ok {
		if previous.RequestHash != requestHash(in) {
			return Task{}, errCode("REQUEST_CONFLICT", "request_id belongs to different task parameters")
		}
		return previous, nil
	}
	var id string
	err := m.change(func(s *Snapshot) error {
		source, valid := referenceSource(in.From)
		if !valid || strings.TrimSpace(in.RequestID) == "" || len(in.RequestID) > 128 {
			return errCode("INVALID_ARGUMENT", "A verified caller and request_id (1-128 bytes) are required")
		}
		hash := requestHash(in)
		if _, exists := s.Operations[in.From+"\n"+in.RequestID]; exists {
			return errCode("REQUEST_CONFLICT", "request_id belongs to a different operation")
		}
		for _, t := range s.Tasks {
			if t.From == in.From && t.RequestID == in.RequestID {
				if t.RequestHash != hash {
					return errCode("REQUEST_CONFLICT", "request_id belongs to different task parameters")
				}
				id = t.ID
				return nil
			}
		}
		if !backendPattern.MatchString(in.Agent) || !filepath.IsAbs(in.Workspace) || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > 1<<20 || len(in.Snapshot) > len(in.Prompt)+(256<<10) {
			return errCode("INVALID_ARGUMENT", "Invalid agent, workspace, task, or context size")
		}
		depth, err := sessionDepth(*s, in.From)
		if err != nil {
			return err
		}
		nested := depth > 0
		depth++
		if in.ParentTaskID != "" {
			parent, ok := s.Tasks[in.ParentTaskID]
			if !ok || parent.Target != in.From || !Occupies(parent.State) {
				return errCode("INVALID_ARGUMENT", "The parent task is not this caller's active execution")
			}
			if parent.CancelRequested {
				return errCode("PARENT_STOPPING", "The initiating task is stopping and cannot accept further child tasks")
			}
			nested = true
			if parent.Depth+1 > depth {
				depth = parent.Depth + 1
			}
		}
		if depth > 2 {
			return errCode("DELEGATION_DEPTH", "Delegation is limited to two levels")
		}
		if in.Target != "" {
			targetSource, ok := referenceSource(in.Target)
			if !ok || targetSource != source || referenceAgent(in.Target) != in.Agent {
				return errCode("INVALID_ARGUMENT", "Target must belong to the caller's Miya instance")
			}
			if ancestor(*s, in.Target, in.From) {
				return errCode("DELEGATION_CYCLE", "Cannot delegate to yourself or an ancestor")
			}
			for parent := in.ParentTaskID; parent != ""; {
				p := s.Tasks[parent]
				if p.From == in.Target || p.Target == in.Target {
					return errCode("DELEGATION_CYCLE", "Cannot delegate back to a task ancestor")
				}
				parent = p.ParentTaskID
			}
			for _, t := range s.Tasks {
				if t.Target == in.Target && !Terminal(t.State) {
					return errCode("THREAD_BUSY", "Target has a managed execution or an unresolved submission")
				}
			}
		}
		if in.PreviousTaskID != "" {
			p, ok := s.Tasks[in.PreviousTaskID]
			if !ok || !canAccess(*s, in.From, p) || in.Target == "" || p.Target != in.Target {
				return errCode("INVALID_ARGUMENT", "Previous task does not belong to this target")
			}
		}
		state := Starting
		if occupied(*s) >= m.limit {
			if nested || in.Target != "" {
				return errCode("QUOTA_EXHAUSTED", "No execution slot is available; this request was not accepted")
			}
			state = Queued
		}
		id = newID("task-")
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = strings.SplitN(strings.TrimSpace(in.Prompt), "\n", 2)[0]
		}
		runes := []rune(title)
		if len(runes) > 120 {
			title = string(runes[:120])
		}
		t := Task{ID: id, RequestID: in.RequestID, RequestHash: hash, From: in.From, Target: in.Target, NewSession: in.Target == "", ParentTaskID: in.ParentTaskID, PreviousTaskID: in.PreviousTaskID, Depth: depth, Agent: in.Agent, Workspace: in.Workspace, Title: title, Prompt: in.Prompt, Snapshot: in.Snapshot, Config: in.Effective, Requested: in.Requested, State: state, SubmissionPhase: "prepared", Result: Result{Availability: "pending"}, CreatedAt: m.now()}
		s.Tasks[id] = t
		if in.Notify {
			s.Watches[watchKey(id, in.From)] = Watch{id, in.From, "watching"}
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return m.Read(id)
}

// ClaimQueued atomically reserves slots before adapters dispatch native work.
func (m *Manager) ClaimQueued() ([]Task, error) {
	view := m.view.Load()
	if view.queued == 0 || view.occupied >= m.limit {
		return nil, nil
	}
	var ids []string
	err := m.change(func(s *Snapshot) error {
		var tasks []Task
		for _, t := range s.Tasks {
			if t.State == Queued {
				tasks = append(tasks, t)
			}
		}
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
				return tasks[i].ID < tasks[j].ID
			}
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		})
		free := m.limit - occupied(*s)
		for _, t := range tasks {
			if free <= 0 {
				break
			}
			t.State = Starting
			s.Tasks[t.ID] = t
			ids = append(ids, t.ID)
			free--
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Task
	for _, id := range ids {
		t, _ := m.Read(id)
		out = append(out, t)
	}
	return out, nil
}
