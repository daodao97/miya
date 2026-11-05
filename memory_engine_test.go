//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/claude"
	"miya/internal/memory"
)

func TestMemoryEngineSerializesAndPausesForUpgrade(t *testing.T) {
	scope, _ := memory.ProjectScope(t.TempDir())
	store := memory.New(filepath.Join(t.TempDir(), "memory"))
	if err := store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []string{"first", "second"} {
		session := testRef(agentClaude, "session").URL()
		job := memory.ExtractionJob{Session: session, TurnID: turn, TaskKey: "session:session", Evidence: []memory.Evidence{{ID: "user", Kind: "user_statement", Text: "项目手机构建固定使用 build-ios.sh", Source: memory.Source{Thread: session, TurnID: turn, MessageID: "user-" + turn, ObservedAt: time.Now()}}}}
		if ok, err := store.EnqueueExtraction(scope, job); err != nil || !ok {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan memory.ExtractionJob, 1)
	release := make(chan struct{})
	saved := make(chan struct{}, 1)
	var calls atomic.Int32
	e := &memoryEngine{store: store, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	e.extract = func(ctx context.Context, _ claude.CLI, j memory.ExtractionJob) ([]memory.Candidate, error) {
		calls.Add(1)
		entered <- j
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return []memory.Candidate{{Action: "add", Kind: "fact", Retention: "decision", Content: "手机构建使用 build-ios.sh", EvidenceID: "user", Quote: j.Evidence[0].Text}}, nil
	}
	e.onSaved = func(memory.Scope, memory.ExtractionJob, []memory.Entry) { saved <- struct{}{} }
	go e.loop()
	select {
	case job := <-entered:
		if job.TurnID != "first" {
			t.Fatal("queue did not preserve completion order")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker never started")
	}
	if !e.busy.Load() {
		t.Fatal("running extraction not counted busy")
	}
	e.pause()
	close(release)
	select {
	case <-saved:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted extraction not finished")
	}
	for deadline := time.Now().Add(time.Second); e.busy.Load() && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	status, err := store.ExtractionStatus(scope)
	if err != nil || calls.Load() != 1 || status.Completed != 1 || status.Pending != 1 || e.busy.Load() {
		t.Fatalf("upgrade pause discarded or started jobs: calls=%d status=%+v busy=%v err=%v", calls.Load(), status, e.busy.Load(), err)
	}
}

func TestMemoryAutomaticSaveOnlyInvalidatesLibrary(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	c.setThreads([]Thread{{ID: 1, ServerID: "session", Messages: []Message{{ID: 10, Role: "user", Content: "项目背景"}, {ID: 11, Role: "assistant", Content: "已处理"}}}})
	scope, _ := memory.ProjectScope(c.cwd)
	now := time.Now()
	e, err := c.memory.Save(scope, memory.Mutation{Kind: "fact", Content: "稳定项目约束", Sources: []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}, ObservedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	engine := newMemoryEngine(c)
	defer engine.cancel()
	before := c.memoryRevision
	engine.onSaved(scope, memory.ExtractionJob{Session: c.threadRef(c.threadWithID(1)).URL()}, []memory.Entry{e})
	d.drain()
	if c.memoryRevision <= before {
		t.Fatal("automatic save did not refresh the library")
	}
	if len(c.threadWithID(1).Messages) != 2 {
		t.Fatal("background extraction inserted a conversation item")
	}
	entries, _, _, err := c.memory.List(scope)
	if err != nil || len(entries) != 1 {
		t.Fatal("background memory is not available in management", err)
	}
}
