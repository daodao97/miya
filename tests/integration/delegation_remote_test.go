//go:build !windows

package integration

import (
	"strings"
	"sync"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/svcproto"
	desktopui "miya/internal/ui"
)

func TestCollaborationWorkspaceSwitchKeepsPendingRequestAtOriginalService(t *testing.T) {
	first := newServiceFixture(t)
	caller := collaborationCaller(t, first, appmodel.AgentClaude, "first-source")
	var from, source string
	first.serv.Do(func() {
		c := first.srv.C
		from, source = c.ThreadRef(c.ThreadWithID(caller)).URL(), c.ReferenceSource
	})
	second := newServiceFixture(t)
	secondSource := "fedcba9876543210fedcba9876543210"
	second.serv.Do(func() { second.srv.C.ReferenceSource = secondSource })
	clientA, err := svcproto.Dial(first.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Close()
	clientB, err := svcproto.Dial(second.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Close()
	var app *desktopui.App
	first.win.Do(func() {
		c := controller.NewMirrorController()
		c.Run, c.Remote, c.ReferenceSource = first.win.Push, clientA, source
		app = &desktopui.App{C: c}
	})
	s := first.srv.C.Collaboration
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	s.Post(func() { close(entered); <-gate })
	<-entered
	first.win.Do(func() {
		p := app.Collaborations.Pane(from)
		p.Submit("start_task", map[string]any{"agent": appmodel.AgentClaude, "task": "original service only", "context": "none", "notify": "none"}, app.CollaborationCall)
	})
	deadline := time.Now().Add(5 * time.Second)
	for s.Pending() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Pending() < 2 {
		t.Fatal("original service did not receive the pending request")
	}
	first.win.Do(func() {
		app.C.(*controller.Controller).Remote, app.C.(*controller.Controller).ReferenceSource = clientB, secondSource
	})
	release()
	first.waitWindow("late response from the original service", func() bool {
		p := app.Collaborations.Pane(from)
		return !p.Busy && p.Pending != nil && strings.Contains(p.Err, "workspace changed")
	})
	if len(second.srv.C.Collaboration.Manager.View().Tasks) != 0 {
		t.Fatal("switching services moved the accepted task to another service")
	}
	first.win.Do(func() {
		app.C.(*controller.Controller).Remote, app.C.(*controller.Controller).ReferenceSource = clientA, source
		app.Collaborations.Pane(from).Retry(app.CollaborationCall)
	})
	first.waitWindow("retry against original service", func() bool {
		p := app.Collaborations.Pane(from)
		return !p.Busy && p.Pending == nil && p.Err == ""
	})
	tasks := first.srv.C.Collaboration.Manager.View().Tasks
	if len(tasks) != 1 {
		t.Fatalf("same request retry created %d tasks at the original service", len(tasks))
	}
	if len(second.srv.C.Collaboration.Manager.View().Tasks) != 0 {
		t.Fatal("retry crossed the source boundary")
	}
	// Both fixtures share process environment. Finish native history writes
	// before the nested fixture restores HOME and removes its temporary tree.
	for id := range tasks {
		task := waitCollaborationTask(t, first, id)
		for deadline := time.Now().Add(5 * time.Second); ; {
			idle := false
			first.serv.Do(func() {
				thread, found := first.srv.C.ThreadByReference(task.Target)
				idle = found && !thread.Busy && first.srv.C.Collaboration.Reservations[thread.ID] == ""
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
