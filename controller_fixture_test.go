package main

// Test fixtures shared by every platform's tests.

import (
	"context"
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
func serverController(dir, claudeBin string) *chatController {
	c := &chatController{referenceSource: testReferenceSource, cwd: dir, ctx: context.Background(), claudeBinary: claudeBin, projectCache: map[string][]Thread{}}
	c.agent, c.setAgent = createSignal(agentCodex)
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.projects, c.setProjects = createSignal([]Project{{Name: "demo", Path: dir}})
	c.threads, c.setThreads = createSignal([]Thread{})
	c.active, c.setActive = createSignal(0)
	c.draft, c.setDraft = createSignal("")
	c.model, c.setModel = createSignal("")
	c.effort, c.setEffort = createSignal("")
	c.permissionPolicy, c.setPermissionPolicy = createSignal("on-request")
	c.locale, c.setLocale = createSignal("en")
	c.models, c.setModels = createSignal([]codex.Model{})
	c.attachments, c.setAttachments = createSignal([]string{})
	c.status, c.setStatus = createSignal("")
	c.ready, c.setReady = createSignal(false)
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	c.historyStatus, c.setHistoryStatus = createSignal("")
	c.revision, c.setRevision = createSignal(0)
	return c
}

func waitCodexResume(t *testing.T, c *chatController, d *testDispatch, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		d.drain()
		if done() {
			return
		}
	}
	t.Fatalf("Codex fixture timed out: status %q, threads %+v", c.status(), c.threads())
}
