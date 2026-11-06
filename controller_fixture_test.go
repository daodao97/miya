package main

// Test fixtures shared by every platform's tests.

import (
	"context"
	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/uiruntime"
	"sync"
	"testing"
	"time"

	"miya/internal/codex"
)

// testDispatch is a controller's own queue of dispatched work, so that a
// run still ending after its test never touches the shared dispatch state.
type testDispatch struct {
	mu sync.Mutex
	q  []func()
}

func (d *testDispatch) push(f func()) { d.mu.Lock(); d.q = append(d.q, f); d.mu.Unlock() }

func (d *testDispatch) take() []func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	q := d.q
	d.q = nil
	return q
}

// drain runs what is dispatched, and what that dispatches, on the test.
func (d *testDispatch) drain() {
	for q := d.take(); len(q) > 0; q = d.take() {
		for _, f := range q {
			f()
		}
	}
}

// serverController is a controller with every signal the service reads.
func serverController(dir, claudeBin string) *controller.Controller {
	c := &controller.Controller{WindowState: appmodel.WindowState{ReferenceSource: testReferenceSource, Cwd: dir, Ctx: context.Background(), ProjectCache: map[string][]appmodel.Thread{}}, ClaudeBinary: claudeBin}
	c.Agent, c.SetAgent = uiruntime.CreateSignal(appmodel.AgentCodex)
	c.DraftPermission, c.SetDraftPermission = uiruntime.CreateSignal("")
	c.Projects, c.SetProjects = uiruntime.CreateSignal([]appmodel.Project{{Name: "demo", Path: dir}})
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]appmodel.Thread{})
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Model, c.SetModel = uiruntime.CreateSignal("")
	c.Effort, c.SetEffort = uiruntime.CreateSignal("")
	c.PermissionPolicy, c.SetPermissionPolicy = uiruntime.CreateSignal("on-request")
	c.Locale, c.SetLocale = uiruntime.CreateSignal("en")
	c.Models, c.SetModels = uiruntime.CreateSignal([]codex.Model{})
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	c.Ready, c.SetReady = uiruntime.CreateSignal(false)
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.HistoryStatus, c.SetHistoryStatus = uiruntime.CreateSignal("")
	c.Revision, c.SetRevision = uiruntime.CreateSignal(0)
	return c
}

func waitCodexResume(t *testing.T, c *controller.Controller, d *testDispatch, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		d.drain()
		if done() {
			return
		}
	}
	t.Fatalf("Codex fixture timed out: status %q, threads %+v", c.Status(), c.Threads())
}
