package delegation

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestExpiredSnapshotRetainsIdempotencyAndIsReadableAfterRestart(t *testing.T) {
	m, now := harness(t, 4)
	in := input("keep-request")
	in.Snapshot = "private context"
	task := create(t, m, in)
	observe(t, m, Event{TaskID: task.ID, Target: ref("child"), State: Completed, Result: Result{Text: "private result"}})
	op := Operation{Caller: task.From, RequestID: "cancel", Name: "cancel_task", TaskID: task.ID}
	receipt, _, err := m.Mutate(op)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * 24 * time.Hour)
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(m.store.dir, "tasks", task.ID))
	if err != nil || len(files) != 0 {
		t.Fatalf("unreferenced payload retained: %v %v", files, err)
	}
	restarted, err := Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := create(t, restarted, in)
	if got.ID != task.ID || got.Result.Availability != "expired" || got.Result.Text != "" || got.Snapshot != "" || got.Prompt != "" {
		t.Fatalf("expired retry: %+v", got)
	}
	if retry, fresh, err := restarted.Mutate(op); err != nil || fresh || !reflect.DeepEqual(retry, receipt) {
		t.Fatalf("receipt expired early: %+v %v", retry, err)
	}
	in.Prompt = "changed"
	if _, err := restarted.Create(in); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatal(err)
	}
}

func TestCollectNeverDropsUnknownExecutionOrPendingEvidence(t *testing.T) {
	m, now := harness(t, 4)
	unknown := create(t, m, input("unknown"))
	observe(t, m, Event{TaskID: unknown.ID, Target: ref("unknown-child"), State: NeedsAttention})
	if err := m.Abandon(unknown.From, unknown.ID, "unknown native outcome"); err != nil {
		t.Fatal(err)
	}
	in := input("notification")
	in.Notify = true
	terminal := create(t, m, in)
	observe(t, m, Event{TaskID: terminal.ID, State: Completed, Result: Result{Text: "still referenced"}})
	*now = now.Add(31 * 24 * time.Hour)
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Read(terminal.ID); got.Result.Text != "still referenced" {
		t.Fatal("pending evidence expired")
	}
	if got, _ := m.Read(unknown.ID); got.State != NeedsAttention || got.Resolution != "abandoned" {
		t.Fatal("unknown execution pruned")
	}
	if err := m.ExpireDeliveries(); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Read(terminal.ID); got.Result.Availability != "expired" {
		t.Fatal("unreferenced expired result not collected")
	}
}

func TestTerminalLimitPrunesOnlyUnreferencedTasksAndKeepsSessionAncestry(t *testing.T) {
	m, now := harness(t, 4)
	m.maxTerminal = 1
	parent := create(t, m, input("parent"))
	observe(t, m, Event{TaskID: parent.ID, Target: ref("child"), State: Completed})
	in := input("nested")
	in.From = ref("child")
	child := create(t, m, in)
	observe(t, m, Event{TaskID: child.ID, Target: ref("grandchild"), State: Running})
	*now = now.Add(time.Minute)
	other := create(t, m, input("other"))
	observe(t, m, Event{TaskID: other.ID, State: Completed})
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	// Session-only ancestry survives even if its original task has been removed.
	if _, err := m.Read(parent.ID); !IsCode(err, "TASK_NOT_FOUND") {
		t.Fatal("old unreferenced task was not pruned")
	}
	*now = now.Add(time.Minute)
	observe(t, m, Event{TaskID: child.ID, Target: ref("grandchild"), State: Completed})
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	in = input("too-deep")
	in.From = ref("grandchild")
	if _, err := m.Create(in); !IsCode(err, "DELEGATION_DEPTH") {
		t.Fatal("GC reset session depth", err)
	}
	if !m.CanAccess(ref("parent"), child.ID) {
		t.Fatal("GC changed ancestor visibility")
	}
}

func TestFailedExpirationCommitDoesNotRemoveReferencedPayload(t *testing.T) {
	m, now := harness(t, 4)
	task := create(t, m, input("failure"))
	observe(t, m, Event{TaskID: task.ID, State: Completed, Result: Result{Text: "saved result"}})
	*now = now.Add(31 * 24 * time.Hour)
	m.store.beforeIndex = func() error { return errors.New("injected index failure") }
	if err := m.Collect(); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	restarted, err := Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Read(task.ID)
	if err != nil || got.Result.Text != "saved result" {
		t.Fatalf("GC removed committed result: %+v %v", got, err)
	}
}
