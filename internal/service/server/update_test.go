package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"miya/internal/async"
	"miya/internal/selfupdate"
	svcproto "miya/internal/service/protocol"
)

type updateHost struct {
	Host
	busy, paused, stopped bool
}

func (h *updateHost) ServiceBusy() bool      { return h.busy }
func (h *updateHost) ServiceKeepAlive() bool { return false }
func (h *updateHost) ServicePause()          { h.paused = true }
func (h *updateHost) ServiceStop()           { h.stopped = true }

type updateBackend struct {
	mu      sync.Mutex
	status  selfupdate.Status
	release chan struct{}
}

func (b *updateBackend) Status() selfupdate.Status                     { b.mu.Lock(); defer b.mu.Unlock(); return b.status }
func (b *updateBackend) Busy() bool                                    { return b.Status().Phase == "installing" }
func (b *updateBackend) StartCheck(context.Context, func(error)) error { return nil }
func (b *updateBackend) StartInstall(_ context.Context, version string, done func(error)) error {
	b.mu.Lock()
	b.status.Phase = "installing"
	b.mu.Unlock()
	go func() {
		<-b.release
		b.mu.Lock()
		b.status.Phase, b.status.Latest = "waiting", version
		b.mu.Unlock()
		done(nil)
	}()
	return nil
}
func (b *updateBackend) ReopenWindows(context.Context) error { return nil }
func (b *updateBackend) ReopenFailed(error)                  {}

func TestMiyaUpdateDoesNotStopInstallingOrBusyService(t *testing.T) {
	host := &updateHost{busy: true}
	ex := async.NewExecutor()
	s := New(host, ex)
	b := &updateBackend{release: make(chan struct{})}
	s.Updates = b
	loopDone := make(chan struct{})
	go func() { ex.Loop(s.Quit); close(loopDone) }()
	t.Cleanup(func() { ex.Do(s.Stop); <-loopDone })
	ex.Do(func() {
		_, err := s.Handle(nil, svcproto.Request{Op: "miyaUpdate", Args: json.RawMessage(`{"Action":"install","Version":"1.2.3"}`)})
		if err != nil {
			t.Error(err)
		}
		if !s.Busy() {
			t.Error("installer was not counted as service work")
		}
		if _, err := s.Handle(nil, svcproto.Request{Op: "quit"}); err == nil {
			t.Error("restart interrupted an installation")
		}
		host.busy = false
		s.QuitWhenIdle = true
		s.CheckIdle()
		if host.stopped {
			t.Error("idle check stopped the installer")
		}
		host.busy = true
	})
	close(b.release)
	for deadline := time.Now().Add(time.Second); ; {
		ready := false
		ex.Do(func() { ready = !s.updateWorking })
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("installation completion was not dispatched")
		}
		time.Sleep(time.Millisecond)
	}
	ex.Do(func() {
		s.CheckIdle()
		if host.stopped || !host.paused || !s.QuitWhenIdle {
			t.Error("running conversations were not preserved for idle restart")
		}
		if s.Updates.Status().Phase != "waiting" {
			t.Error("installed version was reported as running")
		}
		host.busy = false
	})
	// Cleanup calls Stop on the executor; normal idle exit is checked here
	// without stopping the loop before cleanup can run.
	ex.Do(func() {
		if s.Busy() {
			t.Error("completed work still blocks restart")
		}
	})
}
