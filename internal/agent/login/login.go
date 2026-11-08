// Package login coordinates native CLI sign-in attempts on the machine that
// owns the credentials. Tokens and process output never cross its boundary.
package login

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"
)

type Request struct {
	Action, Agent, SessionID, Method string
	Code                             string `json:",omitempty"`
}

type Identity struct {
	LoggedIn            bool
	Method, Email, Plan string
}

type Challenge struct{ URL, UserCode string }

type State struct {
	Agent, SessionID, Method, Phase string
	Challenge
	Error     string
	Identity  *Identity
	ExpiresAt time.Time
}

func (s State) Active() bool {
	return s.Phase == "starting" || s.Phase == "waiting" || s.Phase == "submitting"
}

type Flow interface {
	Submit(context.Context, string) error
	Wait(context.Context) error
	Close()
}

type Provider interface {
	Start(context.Context, string) (Flow, Challenge, error)
	Check(context.Context) (Identity, error)
}

type attempt struct {
	state  State
	flow   Flow
	ctx    context.Context
	cancel context.CancelFunc
}

type Manager struct {
	ctx       context.Context
	providers map[string]Provider
	mu        sync.Mutex
	attempts  map[string]*attempt
}

func New(ctx context.Context, providers map[string]Provider) *Manager {
	return &Manager{ctx: ctx, providers: providers, attempts: make(map[string]*attempt)}
}

// SetProvider updates the client used by future operations. An active attempt
// retains the native process that generated its authorization challenge.
func (m *Manager) SetProvider(agent string, provider Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[agent] = provider
}

// Do returns promptly for start/submit. Polling is read-only, and a request's
// session ID prevents a late code or cancellation from reaching a newer flow.
func (m *Manager) Do(r Request) (State, error) {
	m.mu.Lock()
	p := m.providers[r.Agent]
	m.mu.Unlock()
	if p == nil {
		return State{}, errors.New("This agent does not support native sign-in")
	}
	if r.Action == "check" {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		identity, err := p.Check(ctx)
		if err != nil {
			return State{}, err
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		s := State{Agent: r.Agent}
		if a := m.attempts[r.Agent]; a != nil && a.state.SessionID == r.SessionID {
			s = a.state
		}
		s.Identity = &identity
		return s, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return State{}, errors.New("Sign-in service has stopped")
	}
	if r.Action == "start" {
		if r.Method == "" {
			r.Method = "browser"
			if r.Agent == "codex" {
				r.Method = "device"
			}
		}
		if (r.Agent == "codex" && r.Method != "device") || (r.Agent == "claude" && r.Method != "browser") {
			return State{}, errors.New("Unsupported sign-in method")
		}
		if a := m.attempts[r.Agent]; a != nil && a.state.Active() {
			return a.state, nil
		}
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
		a := &attempt{ctx: ctx, cancel: cancel, state: State{Agent: r.Agent, Method: r.Method, SessionID: rand.Text(), Phase: "starting", ExpiresAt: time.Now().Add(10 * time.Minute)}}
		m.attempts[r.Agent] = a
		go m.run(a, p)
		return a.state, nil
	}
	a := m.attempts[r.Agent]
	if a == nil || r.SessionID == "" || a.state.SessionID != r.SessionID {
		return State{}, errors.New("Sign-in attempt expired. Generate a new link.")
	}
	switch r.Action {
	case "status":
		return a.state, nil
	case "cancel":
		if a.state.Active() {
			m.finish(a, "cancelled", "")
			a.cancel()
		}
		return a.state, nil
	case "submit":
		code := strings.TrimSpace(r.Code)
		if code == "" || len(code) > 16<<10 || strings.ContainsAny(code, "\r\n\x00\x1b") {
			return State{}, errors.New("Paste a single authorization code")
		}
		if a.state.Phase != "waiting" || a.flow == nil || a.state.Method == "device" {
			return State{}, errors.New("This sign-in attempt is not waiting for a code")
		}
		a.state.Phase, a.state.Error = "submitting", ""
		go func() {
			err := a.flow.Submit(a.ctx, code)
			m.mu.Lock()
			defer m.mu.Unlock()
			if err != nil && a.state.Phase == "submitting" {
				a.state.Phase, a.state.Error = "waiting", err.Error()
			}
		}()
		return a.state, nil
	}
	return State{}, errors.New("Unknown sign-in operation")
}

func (m *Manager) run(a *attempt, p Provider) {
	defer a.cancel()
	flow, challenge, err := p.Start(a.ctx, a.state.Method)
	if err == nil {
		defer flow.Close()
		m.mu.Lock()
		if a.state.Active() {
			a.flow, a.state.Challenge, a.state.Phase = flow, challenge, "waiting"
		}
		m.mu.Unlock()
		err = flow.Wait(a.ctx)
	}
	var identity Identity
	if err == nil {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		identity, err = p.Check(ctx)
		cancel()
		if err == nil && !identity.LoggedIn {
			err = errors.New("Native sign-in completed, but the CLI is not signed in. Check again.")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !a.state.Active() {
		return
	}
	if a.ctx.Err() != nil {
		m.finish(a, "failed", "Sign-in expired. Generate a new link.")
	} else if err != nil {
		m.finish(a, "failed", err.Error())
	} else {
		m.finish(a, "succeeded", "")
		a.state.Identity = &identity
	}
}

// finish is called under mu. Drop one-time links as soon as they stop working.
func (m *Manager) finish(a *attempt, phase, message string) {
	a.state.Phase, a.state.Error, a.state.Challenge = phase, message, Challenge{}
}

func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.attempts {
		if a.state.Active() {
			return true
		}
	}
	return false
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.attempts {
		if a.state.Active() {
			m.finish(a, "cancelled", "")
			a.cancel()
		}
	}
}
