//go:build !windows

package extractor

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/memory"
	"miya/internal/model"
)

func TestMemoryEngineSerializesAndPausesForUpgrade(t *testing.T) {
	scope, _ := memory.ProjectScope(t.TempDir())
	store := memory.New(filepath.Join(t.TempDir(), "memory"))
	if err := store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []string{"first", "second"} {
		session := testRef(model.AgentClaude, "session").URL()
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
	e := &Engine{store: store, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
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
	e.Pause()
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
