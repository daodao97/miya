// Package tracker follows the background work of each conversation: its
// native tasks, the processes it started and the services they listen on,
// remembering them across restarts and answering what the views and phones
// ask. internal/runtime holds the data and probes it builds on.
package tracker

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"miya/internal/chat/model"
	"miya/internal/remote"
	rt "miya/internal/runtime"
	webservice "miya/internal/runtime/web"
)

type ConversationRuntime struct {
	View     rt.View              `json:"view"`
	Services []webservice.Service `json:"services"`
}

// Deadline-free call sites use the same stable request identity as the phone.
func RemoteRuntimeRequest(ref model.ThreadRef, project string) remote.Request {
	return remote.Request{ServerID: ref.SessionID, Agent: ref.Backend, Project: project}
}

type Request struct {
	Ref     model.ThreadRef
	Project string
}

func Result(manager *Manager, key rt.SessionKey, view rt.View) ConversationRuntime {
	result := ConversationRuntime{View: view, Services: []webservice.Service{}}
	for _, e := range view.Endpoints {
		if e.Protocol != "http" {
			continue
		}
		name := "Web services"
		for _, p := range view.Processes {
			if p.Identity == e.Process {
				name = p.Name
				break
			}
		}
		result.Services = append(result.Services, webservice.Service{Port: e.Port, Host: e.Host, Process: name, RuntimeToken: manager.Token(key, e), Owner: &model.ThreadRef{Source: key.Source, Backend: key.Backend, SessionID: key.SessionID}})
	}
	return result
}

func NewManager(ctx context.Context, path string) *Manager {
	watch := ctx != nil && ctx.Done() != nil
	if ctx == nil {
		ctx = context.Background()
	}
	owned, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: owned, cancel: cancel, path: path, store: rt.New(), sources: map[rt.SessionKey]*Binding{}, flights: map[rt.SessionKey]*Flight{}}
	if path != "" {
		m.store = rt.Load(path)
	}
	if _, err := rand.Read(m.secret[:]); err != nil {
		panic(err)
	}
	if watch {
		m.wg.Add(1)
		go func() { defer m.wg.Done(); m.watch() }()
	}
	return m
}

type Binding struct {
	source     SnapshotSource
	wake, last time.Time
}

// Endpoint tokens bind a preview to a session and an exact observed process.
// The same port subsequently owned by another process cannot reuse the token.
type EndpointToken struct {
	Key      rt.SessionKey
	Endpoint rt.Endpoint
	Expires  int64
}

type Flight struct{ done chan struct{} }

func KeyOf(ref model.ThreadRef) rt.SessionKey {
	return rt.SessionKey{Source: ref.Source, Backend: ref.Backend, SessionID: ref.SessionID}
}

type Manager struct {
	store     *rt.Store
	ctx       context.Context
	path      string
	secret    [32]byte
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
	lastSaved time.Time
	mu        sync.Mutex
	sources   map[rt.SessionKey]*Binding
	flights   map[rt.SessionKey]*Flight
	observer  Observer
}

func (m *Manager) Close() {
	m.mu.Lock()
	already := m.closed
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	if !already && m.path != "" {
		_ = m.store.Save(m.path)
	}
}

func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.flights) > 0 {
		return true
	}
	for key, b := range m.sources {
		if !b.source.Caps.RequiresHost || b.source.Alive != nil && !b.source.Alive() {
			continue
		}
		view := m.store.Read(key)
		if !view.TasksKnown {
			for _, p := range view.Processes {
				if p.Status == "running" {
					return true
				}
			}
		}
		for _, task := range view.Tasks {
			if rt.Active(task.Status) {
				return true
			}
		}
	}
	return false
}

func (m *Manager) Kick(key rt.SessionKey) { go m.Refresh(m.ctx, key) }

func (m *Manager) Persist() {
	if m.path == "" {
		return
	}
	m.mu.Lock()
	save := !m.closed && time.Since(m.lastSaved) >= time.Second
	if save {
		m.lastSaved = time.Now()
	}
	m.mu.Unlock()
	if save {
		_ = m.store.Save(m.path)
	}
}

func (m *Manager) Refresh(ctx context.Context, key rt.SessionKey) rt.View {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return m.store.Read(key)
	}
	if flight := m.flights[key]; flight != nil {
		m.mu.Unlock()
		select {
		case <-flight.done:
		case <-ctx.Done():
		}
		return m.store.Read(key)
	}
	binding := m.sources[key]
	if binding == nil {
		m.mu.Unlock()
		v := m.store.Read(key)
		v.Notice = "No agent execution connection available for verification"
		return v
	}
	source := binding.source
	binding.last = time.Now()
	flight := &Flight{done: make(chan struct{})}
	m.flights[key] = flight
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.flights, key); close(flight.done); m.mu.Unlock(); m.wg.Done() }()
	work, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	snapshot := TaskSnapshot{Notice: "Agent connection ended. Verifying recorded processes."}
	var err error
	alive := source.Alive == nil || source.Alive()
	if alive && source.Snapshot == nil {
		err = errors.New("Runtime adapter does not support snapshots")
	} else if alive {
		snapshot, err = source.Snapshot(work)
	}
	if err != nil {
		snapshot.Notice = "Native background information unavailable. Keeping the last process verification."
	}
	if !snapshot.Known && err == nil {
		for _, task := range snapshot.Tasks {
			m.store.PartialTask(key, source.Epoch, task)
		}
	}
	m.store.Tasks(key, source.Epoch, snapshot.Tasks, snapshot.Known && err == nil, snapshot.Notice)
	if !alive || err != nil {
		m.store.TasksUnknown(key, source.Epoch)
	}
	all, err := m.observer.processes(false)
	if err != nil {
		m.store.Unknown(key, source.Epoch, "Unable to verify system processes: "+err.Error())
		return m.store.Read(key)
	}
	if source.Caps.SessionTag {
		snapshot.Roots = append(snapshot.Roots, TaggedRoots(work, key, all)...)
		// The marker was read asynchronously; join it only to the same identity.
		all, err = m.observer.processes(true)
		if err != nil {
			m.store.Unknown(key, source.Epoch, "Unable to reverify system processes")
			return m.store.Read(key)
		}
	}
	m.store.Observe(key, source.Epoch, all, snapshot.Roots, nil)
	view := m.store.Read(key)
	endpoints, err := Listeners(work, view.Processes)
	if err == nil {
		endpoints, err = rt.ProbeHTTP(work, endpoints)
	}
	if err != nil {
		m.store.Unknown(key, source.Epoch, "Process found, but listening ports cannot be verified yet")
		return m.store.Read(key)
	}
	// Recheck identities after the asynchronous probe; do not attach a reused PID.
	all, err = m.observer.processes(true)
	if err != nil {
		m.store.Unknown(key, source.Epoch, "Unable to reverify system processes")
		return m.store.Read(key)
	}
	m.store.Observe(key, source.Epoch, all, nil, endpoints)
	if work.Err() == nil {
		m.Persist()
	}
	return m.store.Read(key)
}

func (m *Manager) Register(key rt.SessionKey, source SnapshotSource) {
	if key.SessionID == "" || source.Epoch == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	old := m.sources[key]
	if old == nil || old.source.Epoch != source.Epoch {
		m.sources[key] = &Binding{source: source, wake: time.Now()}
		m.store.Begin(key, source.Epoch, source.Caps)
	} else {
		old.wake = time.Now()
	}
}

func (m *Manager) Token(key rt.SessionKey, endpoint rt.Endpoint) string {
	data, _ := json.Marshal(EndpointToken{key, endpoint, time.Now().Add(30 * time.Minute).Unix()})
	mac := hmac.New(sha256.New, m.secret[:])
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), data...))
}

func (m *Manager) ValidateToken(ctx context.Context, token, host string, port int) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return errors.New("Invalid service access token")
	}
	mac := hmac.New(sha256.New, m.secret[:])
	mac.Write(raw[sha256.Size:])
	if !hmac.Equal(raw[:sha256.Size], mac.Sum(nil)) {
		return errors.New("Service access token is no longer valid. Refresh the list.")
	}
	var claim EndpointToken
	if json.Unmarshal(raw[sha256.Size:], &claim) != nil || claim.Expires < time.Now().Unix() || claim.Endpoint.Host != host || claim.Endpoint.Port != port {
		return errors.New("Service access token expired or target mismatch")
	}
	all, err := m.observer.processes(true)
	if err != nil {
		return err
	}
	present := false
	for _, p := range all {
		if p.Identity == claim.Endpoint.Process {
			present = true
			break
		}
	}
	if !present {
		return errors.New("Original service process ended. Refresh the list.")
	}
	endpoints, err := Listeners(ctx, []rt.Process{{Identity: claim.Endpoint.Process, Status: "running"}})
	if err != nil {
		return err
	}
	for _, e := range endpoints {
		if e.Host == host && e.Port == port && e.Process == claim.Endpoint.Process {
			return nil
		}
	}
	return fmt.Errorf("Original process is no longer listening on port %d. Refresh the list.", port)
}

func (m *Manager) watch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.Lock()
			keys := []rt.SessionKey{}
			for key, b := range m.sources {
				view := m.store.Read(key)
				active := false
				for _, t := range view.Tasks {
					active = active || rt.Active(t.Status)
				}
				for _, p := range view.Processes {
					active = active || p.Status == "running" || p.Status == "unknown"
				}
				recent := now.Sub(b.wake) < 10*time.Second
				interval := 10 * time.Second
				if recent {
					interval = time.Second
				}
				if (active || recent) && now.Sub(b.last) >= interval {
					keys = append(keys, key)
				}
			}
			m.mu.Unlock()
			for _, key := range keys {
				m.Kick(key)
			}
		}
	}
}

type TaskSnapshot struct {
	Tasks  []rt.Task
	Known  bool
	Roots  []rt.Root
	Notice string
}

type SnapshotSource struct {
	Epoch    string
	Caps     rt.Capabilities
	Alive    func() bool
	Snapshot func(context.Context) (TaskSnapshot, error)
}

// Context ends when the manager closes.
func (m *Manager) Context() context.Context { return m.ctx }

// Store is where observed runtimes are kept across restarts.
func (m *Manager) Store() *rt.Store { return m.store }

// LastObserved is when key's runtime was last observed; key must be
// registered.
func (m *Manager) LastObserved(key rt.SessionKey) time.Time {
	m.mu.Lock()
	last := m.sources[key].last
	m.mu.Unlock()
	return last
}

func Listeners(ctx context.Context, processes []rt.Process) ([]rt.Endpoint, error) {
	owned := map[int]rt.Identity{}
	pids := []string{}
	for _, p := range processes {
		if p.Status == "running" {
			owned[p.Identity.PID] = p.Identity
			pids = append(pids, strconv.Itoa(p.Identity.PID))
		}
	}
	if len(pids) == 0 {
		return []rt.Endpoint{}, nil
	}
	data, err := webservice.RunLSOF(ctx, "-nP", "-a", "-p", strings.Join(pids, ","), "-iTCP", "-sTCP:LISTEN", "-F0pcfnt")
	if err != nil {
		return nil, err
	}
	return rt.ParseListeners(data, owned), nil
}

type Observer struct {
	mu  sync.Mutex
	at  time.Time
	all []rt.Process
	err error
}

func (o *Observer) processes(force bool) ([]rt.Process, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !force && time.Since(o.at) < 200*time.Millisecond {
		return o.all, o.err
	}
	o.all, o.err = rt.SystemProcesses()
	o.at = time.Now()
	return o.all, o.err
}

// Only inspect same-user listening candidates, never the environment of the
// full process table. A detached service can retain Codex's execution marker
// after its shell and native background terminal have both ended.
func TaggedRoots(ctx context.Context, key rt.SessionKey, all []rt.Process) []rt.Root {
	if key.Backend != model.AgentCodex || key.SessionID == "" {
		return nil
	}
	data, err := webservice.RunLSOF(ctx, "-nP", "-a", "-u", strconv.Itoa(os.Getuid()), "-iTCP", "-sTCP:LISTEN", "-F0p")
	if err != nil {
		return nil
	}
	byPID := map[int]rt.Process{}
	for _, p := range all {
		byPID[p.Identity.PID] = p
	}
	var roots []rt.Root
	for _, field := range webservice.LSOFFields(data) {
		if !strings.HasPrefix(field, "p") {
			continue
		}
		pid, err := strconv.Atoi(field[1:])
		p, ok := byPID[pid]
		if err != nil || !ok || ctx.Err() != nil {
			continue
		}
		if rt.CodexProcessSession(pid) == key.SessionID {
			roots = append(roots, rt.Root{PID: pid, TaskID: "_session-tag/" + strconv.Itoa(pid) + "/" + p.Identity.Start, Identity: p.Identity, Evidence: "session-tag"})
		}
	}
	return roots
}
