//go:build !windows

package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCollaborationWorkspaceSwitchKeepsPendingRequestAtOriginalService(t *testing.T) {
	first := newServiceFixture(t)
	caller := collaborationCaller(t, first, agentClaude, "first-source")
	var from, source string
	first.serv.do(func() {
		c := first.srv.c
		from, source = c.threadRef(c.threadWithID(caller)).URL(), c.referenceSource
	})
	second := newServiceFixture(t)
	secondSource := "fedcba9876543210fedcba9876543210"
	second.serv.do(func() { second.srv.c.referenceSource = secondSource })
	clientA, err := dialService(first.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Close()
	clientB, err := dialService(second.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Close()
	var app *chatApp
	first.win.do(func() {
		c := newMirrorController()
		c.run, c.remote, c.referenceSource = first.win.push, clientA, source
		app = &chatApp{c: c}
	})
	s := first.srv.c.collaboration
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	s.post(func() { close(entered); <-gate })
	<-entered
	first.win.do(func() {
		p := app.collaborations.pane(from)
		p.agent, p.prompt = agentClaude, "original service only"
		p.notify = false
		p.start(app.collaborationCall)
	})
	deadline := time.Now().Add(5 * time.Second)
	for s.busy.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.busy.Load() < 2 {
		t.Fatal("original service did not receive the pending request")
	}
	first.win.do(func() { app.c.remote, app.c.referenceSource = clientB, secondSource })
	release()
	first.waitWindow("late response from the original service", func() bool {
		p := app.collaborations.pane(from)
		return !p.busy && p.pending != nil && strings.Contains(p.err, "workspace changed")
	})
	if len(second.srv.c.collaboration.manager.View().Tasks) != 0 {
		t.Fatal("switching services moved the accepted task to another service")
	}
	first.win.do(func() {
		app.c.remote, app.c.referenceSource = clientA, source
		app.collaborations.pane(from).retry(app.collaborationCall)
	})
	first.waitWindow("retry against original service", func() bool {
		p := app.collaborations.pane(from)
		return !p.busy && p.pending == nil && p.err == ""
	})
	tasks := first.srv.c.collaboration.manager.View().Tasks
	if len(tasks) != 1 {
		t.Fatalf("same request retry created %d tasks at the original service", len(tasks))
	}
	if len(second.srv.c.collaboration.manager.View().Tasks) != 0 {
		t.Fatal("retry crossed the source boundary")
	}
	// Both fixtures share process environment. Finish native history writes
	// before the nested fixture restores HOME and removes its temporary tree.
	for id := range tasks {
		task := waitCollaborationTask(t, first, id)
		for deadline := time.Now().Add(5 * time.Second); ; {
			idle := false
			first.serv.do(func() {
				thread, found := first.srv.c.threadByReference(task.Target)
				idle = found && !thread.Busy && first.srv.c.managedReservations[thread.ID] == ""
			})
			if idle {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("native task did not finish before fixture cleanup")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
