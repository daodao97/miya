package login

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeFlow struct {
	waiting, submitted, closed chan struct{}
	complete                   chan error
}

func newFlow() *fakeFlow {
	return &fakeFlow{make(chan struct{}), make(chan struct{}, 1), make(chan struct{}), make(chan error, 1)}
}
func (f *fakeFlow) Submit(ctx context.Context, code string) error {
	if code != "valid#state" {
		return errors.New("wrong state")
	}
	f.submitted <- struct{}{}
	return nil
}
func (f *fakeFlow) Wait(ctx context.Context) error {
	close(f.waiting)
	select {
	case err := <-f.complete:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *fakeFlow) Close() { close(f.closed) }

type fakeProvider struct{ flows chan *fakeFlow }

func (p fakeProvider) Start(ctx context.Context, method string) (Flow, Challenge, error) {
	f := <-p.flows
	return f, Challenge{URL: "https://claude.ai/oauth/authorize?state=state"}, nil
}
func (p fakeProvider) Check(context.Context) (Identity, error) {
	return Identity{LoggedIn: true, Email: "prior@example.invalid"}, nil
}

func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("native fixture stalled")
	}
}
func waitState(t *testing.T, m *Manager, id string, predicate func(State) bool) State {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		s, err := m.Do(Request{Agent: "claude", Action: "status", SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		if predicate(s) {
			return s
		}
		if ctx.Err() != nil {
			t.Fatalf("state did not settle: %+v", s)
		}
		runtime.Gosched()
	}
}

func TestLoginCannotCompleteFromAnExistingAccountAndRejectsStaleCodes(t *testing.T) {
	flows := make(chan *fakeFlow, 2)
	first, second := newFlow(), newFlow()
	flows <- first
	flows <- second
	m := New(context.Background(), map[string]Provider{"claude": fakeProvider{flows}})
	defer m.Close()
	state, err := m.Do(Request{Action: "start", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	await(t, first.waiting)
	checked, err := m.Do(Request{Action: "check", Agent: "claude", SessionID: state.SessionID})
	if err != nil || checked.Phase != "waiting" || !checked.Identity.LoggedIn {
		t.Fatal("old credentials completed pending login", checked, err)
	}
	if _, err := m.Do(Request{Action: "submit", Agent: "claude", SessionID: state.SessionID, Code: "bad#state"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, state.SessionID, func(s State) bool { return s.Phase == "waiting" && s.Error != "" })
	if _, err := m.Do(Request{Action: "cancel", Agent: "claude", SessionID: state.SessionID}); err != nil {
		t.Fatal(err)
	}
	await(t, first.closed)
	next, err := m.Do(Request{Action: "start", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	await(t, second.waiting)
	if _, err := m.Do(Request{Action: "submit", Agent: "claude", SessionID: state.SessionID, Code: "valid#state"}); err == nil {
		t.Fatal("stale code reached a new login")
	}
	if _, err := m.Do(Request{Action: "cancel", Agent: "claude", SessionID: state.SessionID}); err == nil {
		t.Fatal("stale cancel reached a new login")
	}
	if _, err := m.Do(Request{Action: "submit", Agent: "claude", SessionID: next.SessionID, Code: "valid#state"}); err != nil {
		t.Fatal(err)
	}
	await(t, second.submitted)
	second.complete <- nil
	completed := waitState(t, m, next.SessionID, func(s State) bool { return !s.Active() })
	if completed.Phase != "succeeded" || completed.URL != "" || completed.UserCode != "" || completed.Identity == nil || !completed.Identity.LoggedIn {
		t.Fatalf("completion was not verified or retained its one-time link: %+v", completed)
	}
}

func TestLoginCancellationAndDeviceCodeDirection(t *testing.T) {
	f := newFlow()
	flows := make(chan *fakeFlow, 1)
	flows <- f
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx, map[string]Provider{"claude": fakeProvider{flows}, "codex": fakeProvider{flows}})
	state, err := m.Do(Request{Action: "start", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	await(t, f.waiting)
	if state.Method != "device" {
		t.Fatal("Codex did not use device login")
	}
	_, err = m.Do(Request{Action: "submit", Agent: "codex", SessionID: state.SessionID, Code: "device-code"})
	if err == nil {
		t.Fatal("device code was submitted to the CLI instead of the official site")
	}
	cancel()
	await(t, f.closed)
	m.mu.Lock()
	phase := m.attempts["codex"].state.Phase
	m.mu.Unlock()
	if phase != "failed" || m.Busy() {
		t.Fatal("cancelled owner left a live login")
	}
	if _, err = m.Do(Request{Action: "start", Agent: "codex", Method: "browser"}); err == nil || strings.Contains(err.Error(), "device-code") {
		t.Fatal("unexpected new login after shutdown")
	}
}
