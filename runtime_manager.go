package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	rt "miya/internal/runtime"
)

type runtimeSnapshot struct {
	Tasks  []rt.Task
	Known  bool
	Roots  []rt.Root
	Notice string
}
type runtimeSource struct {
	Epoch    string
	Caps     rt.Capabilities
	Alive    func() bool
	Snapshot func(context.Context) (runtimeSnapshot, error)
}
type runtimeBinding struct {
	source     runtimeSource
	wake, last time.Time
}
type runtimeFlight struct{ done chan struct{} }
type runtimeManager struct {
	store     *rt.Store
	ctx       context.Context
	path      string
	secret    [32]byte
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
	lastSaved time.Time
	mu        sync.Mutex
	sources   map[rt.SessionKey]*runtimeBinding
	flights   map[rt.SessionKey]*runtimeFlight
	observer  runtimeObserver
}

func newRuntimeManager(ctx context.Context, path string) *runtimeManager {
	watch := ctx != nil && ctx.Done() != nil
	if ctx == nil {
		ctx = context.Background()
	}
	owned, cancel := context.WithCancel(ctx)
	m := &runtimeManager{ctx: owned, cancel: cancel, path: path, store: rt.New(), sources: map[rt.SessionKey]*runtimeBinding{}, flights: map[rt.SessionKey]*runtimeFlight{}}
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
func (m *runtimeManager) Close() {
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
func (m *runtimeManager) Persist() {
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
func (c *chatController) runtimes() *runtimeManager {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.runtime == nil {
		c.runtime = newRuntimeManager(c.ctx, c.runtimePath)
		if c.cancel != nil {
			cancel := c.cancel
			manager := c.runtime
			c.cancel = func() { cancel(); manager.Close() }
		}
	}
	return c.runtime
}
func runtimeKey(ref ThreadRef) rt.SessionKey {
	return rt.SessionKey{Source: ref.Source, Backend: ref.Backend, SessionID: ref.SessionID}
}
func (m *runtimeManager) Register(key rt.SessionKey, source runtimeSource) {
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
		m.sources[key] = &runtimeBinding{source: source, wake: time.Now()}
		m.store.Begin(key, source.Epoch, source.Caps)
	} else {
		old.wake = time.Now()
	}
}
func (m *runtimeManager) Kick(key rt.SessionKey) { go m.Refresh(m.ctx, key) }
func (m *runtimeManager) Refresh(ctx context.Context, key rt.SessionKey) rt.View {
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
	flight := &runtimeFlight{done: make(chan struct{})}
	m.flights[key] = flight
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.flights, key); close(flight.done); m.mu.Unlock(); m.wg.Done() }()
	work, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	snapshot := runtimeSnapshot{Notice: "Agent connection ended. Verifying recorded processes."}
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
		snapshot.Roots = append(snapshot.Roots, runtimeTaggedRoots(work, key, all)...)
		// The marker was read asynchronously; join it only to the same identity.
		all, err = m.observer.processes(true)
		if err != nil {
			m.store.Unknown(key, source.Epoch, "Unable to reverify system processes")
			return m.store.Read(key)
		}
	}
	m.store.Observe(key, source.Epoch, all, snapshot.Roots, nil)
	view := m.store.Read(key)
	endpoints, err := runtimeListeners(work, view.Processes)
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
func (m *runtimeManager) watch() {
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
func (m *runtimeManager) Busy() bool {
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

// Endpoint tokens bind a preview to a session and an exact observed process.
// The same port subsequently owned by another process cannot reuse the token.
type runtimeEndpointToken struct {
	Key      rt.SessionKey
	Endpoint rt.Endpoint
	Expires  int64
}

func (m *runtimeManager) Token(key rt.SessionKey, endpoint rt.Endpoint) string {
	data, _ := json.Marshal(runtimeEndpointToken{key, endpoint, time.Now().Add(30 * time.Minute).Unix()})
	mac := hmac.New(sha256.New, m.secret[:])
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), data...))
}
func (m *runtimeManager) ValidateToken(ctx context.Context, token, host string, port int) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return errors.New("Invalid service access token")
	}
	mac := hmac.New(sha256.New, m.secret[:])
	mac.Write(raw[sha256.Size:])
	if !hmac.Equal(raw[:sha256.Size], mac.Sum(nil)) {
		return errors.New("Service access token is no longer valid. Refresh the list.")
	}
	var claim runtimeEndpointToken
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
	endpoints, err := runtimeListeners(ctx, []rt.Process{{Identity: claim.Endpoint.Process, Status: "running"}})
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

func (c *chatController) runtimeBusy() bool {
	c.runtimeMu.Lock()
	manager := c.runtime
	c.runtimeMu.Unlock()
	return manager != nil && manager.Busy()
}

// Context ends when the manager closes.
func (m *runtimeManager) Context() context.Context { return m.ctx }

// Store is where observed runtimes are kept across restarts.
func (m *runtimeManager) Store() *rt.Store { return m.store }

// LastObserved is when key's runtime was last observed; key must be
// registered.
func (m *runtimeManager) LastObserved(key rt.SessionKey) time.Time {
	m.mu.Lock()
	last := m.sources[key].last
	m.mu.Unlock()
	return last
}
