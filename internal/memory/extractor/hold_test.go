//go:build !windows

package extractor

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/chat/model"
	"miya/internal/memory"
)

func TestMemoryEngineWaitsWhileHeld(t *testing.T) {
	scope, _ := memory.ProjectScope(t.TempDir())
	store := memory.New(filepath.Join(t.TempDir(), "memory"))
	if err := store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	session := testRef(model.AgentClaude, "session").URL()
	job := memory.ExtractionJob{Session: session, TurnID: "turn", TaskKey: "session:session", Evidence: []memory.Evidence{{ID: "user", Kind: "user_statement", Text: "项目手机构建固定使用 build-ios.sh", Source: memory.Source{Thread: session, TurnID: "turn", MessageID: "user-turn", ObservedAt: time.Now()}}}}
	if ok, err := store.EnqueueExtraction(scope, job); err != nil || !ok {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var held atomic.Bool
	held.Store(true)
	ran := make(chan struct{}, 1)
	e := &Engine{store: store, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	e.extract = func(context.Context, claude.CLI, memory.ExtractionJob) ([]memory.Candidate, error) {
		ran <- struct{}{}
		return nil, nil
	}
	e.SetHold(held.Load)
	done := make(chan struct{})
	go func() { defer close(done); e.loop() }()
	// Stop the loop before the temporary directories are removed.
	defer func() { cancel(); <-done }()
	e.Wakeup()
	select {
	case <-ran:
		t.Fatal("extracted while held")
	case <-time.After(300 * time.Millisecond):
	}
	if status, _ := store.ExtractionStatus(scope); status.Pending != 1 {
		t.Fatalf("held job left the queue: %+v", status)
	}
	held.Store(false)
	e.Wakeup()
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("job never ran after the hold ended")
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if status, _ := store.ExtractionStatus(scope); status.Completed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never completed")
		}
	}
}
