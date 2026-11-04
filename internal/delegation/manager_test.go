package delegation

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func ref(id string) string { return "miya://0123456789abcdef0123456789abcdef/thread/codex/" + id }
func harness(t *testing.T, limit int) (*Manager, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	m, err := Open(NewStore(t.TempDir()), Options{Limit: limit, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return m, &now
}
func input(request string) StartInput {
	return StartInput{From: ref("parent"), RequestID: request, Agent: "codex", Workspace: filepath.Clean("/workspace"), Prompt: "review the change", Title: "Review"}
}
func create(t *testing.T, m *Manager, in StartInput) Task {
	t.Helper()
	v, err := m.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func observe(t *testing.T, m *Manager, e Event) {
	t.Helper()
	if err := m.Observe(e); err != nil {
		t.Fatal(err)
	}
}

func TestCreateIsDurableIdempotentAndDoesNotReResolveDefaults(t *testing.T) {
	m, _ := harness(t, 4)
	in := input("one")
	in.Effective = Config{Model: "original"}
	in.Snapshot = "first snapshot"
	first := create(t, m, in)
	in.Effective.Model = "new default"
	in.Snapshot = "new snapshot"
	again := create(t, m, in)
	if again.ID != first.ID || again.Config.Model != "original" || again.Snapshot != "first snapshot" {
		t.Fatalf("retry changed accepted task: %+v", again)
	}
	in.Prompt = "different request"
	if _, err := m.Create(in); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatalf("conflicting retry: %v", err)
	}
	loaded, err := m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tasks[first.ID].ID != first.ID {
		t.Fatal("accepted task was not saved")
	}
}

func TestQuotaReservationAndNestedTasksCannotQueueBehindWaitingParents(t *testing.T) {
	m, _ := harness(t, 4)
	var parents []Task
	for i := range 4 {
		in := input(fmt.Sprint(i))
		p := create(t, m, in)
		target := ref(fmt.Sprintf("worker-%d", i))
		observe(t, m, Event{TaskID: p.ID, Target: target, State: Running, NativeTurnID: "turn", SessionEpoch: "epoch"})
		p, _ = m.Read(p.ID)
		parents = append(parents, p)
	}
	for _, p := range parents {
		in := input("child")
		in.From = p.Target
		in.ParentTaskID = p.ID
		if _, err := m.Create(in); !IsCode(err, "QUOTA_EXHAUSTED") {
			t.Fatalf("nested task accepted without capacity: %v", err)
		}
	}
	queued := create(t, m, input("root-queued"))
	if queued.State != Queued {
		t.Fatal(queued.State)
	}
	observe(t, m, Event{TaskID: parents[0].ID, Target: parents[0].Target, State: Completed, NativeTurnID: "turn", SessionEpoch: "epoch"})
	claimed, err := m.ClaimQueued()
	if err != nil || len(claimed) != 1 || claimed[0].ID != queued.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
}

func TestConcurrentAcceptsOnlyReserveAvailableSlots(t *testing.T) {
	m, _ := harness(t, 1)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			in := input(fmt.Sprint(i))
			in.Target = ref(fmt.Sprintf("existing-%d", i))
			_, err := m.Create(in)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
		} else if !IsCode(err, "QUOTA_EXHAUSTED") {
			t.Fatal(err)
		}
	}
	if accepted != 1 || occupied(m.View()) != 1 {
		t.Fatalf("accepted %d, occupied %d", accepted, occupied(m.View()))
	}
}

func TestBoundResultNeverFollowsLaterTurnsOrLateStartAcknowledgements(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("fixed"))
	target := ref("worker")
	observe(t, m, Event{TaskID: task.ID, Target: target, NativeTurnID: "turn-1", SessionEpoch: "epoch-1", State: Completed, Result: Result{Text: "fixed result"}, Sequence: 2})
	observe(t, m, Event{TaskID: task.ID, Target: target, NativeTurnID: "turn-1", SessionEpoch: "epoch-1", State: Running, Sequence: 1})
	if err := m.Observe(Event{TaskID: task.ID, Target: target, NativeTurnID: "turn-2", State: Completed, Result: Result{Text: "wrong result"}, Sequence: 3}); !IsCode(err, "STALE_EVENT") {
		t.Fatal(err)
	}
	if err := m.Observe(Event{TaskID: task.ID, Target: target, NativeTurnID: "turn-1", SessionEpoch: "epoch-2", State: Failed, Sequence: 3}); !IsCode(err, "STALE_EVENT") {
		t.Fatal(err)
	}
	got, _ := m.Read(task.ID)
	if got.State != Completed || got.Result.Text != "fixed result" {
		t.Fatalf("fixed result changed: %+v", got)
	}
	// A follow-up in the same native session is a separate task.
	in := input("follow-up")
	in.Target = target
	next := create(t, m, in)
	observe(t, m, Event{TaskID: next.ID, Target: target, NativeTurnID: "turn-2", State: Completed, Result: Result{Text: "second result"}})
	got, _ = m.Read(task.ID)
	if got.Result.Text != "fixed result" {
		t.Fatal("later conversation mutated old result")
	}
}

func TestUnknownExecutionSurvivesTimeRestartAndAbandonWithoutReleasingSlot(t *testing.T) {
	m, now := harness(t, 1)
	in := input("unknown")
	in.Notify = true
	task := create(t, m, in)
	observe(t, m, Event{TaskID: task.ID, Target: ref("worker"), NativeTurnID: "turn", State: NeedsAttention})
	*now = now.Add(24 * time.Hour)
	if err := m.Abandon(task.From, task.ID, "Cannot verify the disconnected agent"); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(m.store, Options{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(); err != nil {
		t.Fatal(err)
	}
	got, _ := recovered.Read(task.ID)
	if got.State != NeedsAttention || got.Resolution != "abandoned" || occupied(recovered.View()) != 1 {
		t.Fatalf("unknown reservation lost: %+v", got)
	}
	observe(t, recovered, Event{TaskID: task.ID, Target: ref("worker"), NativeTurnID: "turn", State: Completed, Result: Result{Text: "late actual result"}})
	if occupied(recovered.View()) != 0 || len(recovered.View().Deliveries) != 0 {
		t.Fatal("actual termination failed to release slot or restored abandoned notification")
	}
}

func TestCancelQueuedIsTerminalButRunningOnlyRecordsIntent(t *testing.T) {
	m, _ := harness(t, 1)
	running := create(t, m, input("active"))
	queued := create(t, m, input("queued"))
	q, err := m.Cancel(queued.From, queued.ID)
	if err != nil || q.State != Interrupted {
		t.Fatalf("cancel queue: %+v %v", q, err)
	}
	r, err := m.Cancel(running.From, running.ID)
	if err != nil || r.State != Starting || !r.CancelRequested {
		t.Fatalf("cancel active: %+v %v", r, err)
	}
	if _, err := m.Cancel(ref("other"), running.ID); !IsCode(err, "ACCESS_DENIED") {
		t.Fatal(err)
	}
}

func TestResultBoundsAreUTF8AndDoNotIncludeOldReply(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("large"))
	observe(t, m, Event{TaskID: task.ID, State: Completed, Result: Result{Text: strings.Repeat("中", ResultLimit)}})
	got, _ := m.Read(task.ID)
	if !got.Result.Truncated || !utf8.ValidString(got.Result.Text) || len(got.Result.Text) > ResultLimit {
		t.Fatalf("invalid result bound: %+v", got.Result)
	}
	next := create(t, m, input("empty"))
	observe(t, m, Event{TaskID: next.ID, State: Completed})
	got, _ = m.Read(next.ID)
	if got.Result.Availability != "unavailable" || got.Result.Text != "" {
		t.Fatal("empty execution inherited another reply")
	}
}

func TestWriteFailureDoesNotPublishAcceptedWork(t *testing.T) {
	m, _ := harness(t, 1)
	m.store.beforeIndex = func() error { return errors.New("disk unavailable") }
	if _, err := m.Create(input("failed-save")); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	if len(m.View().Tasks) != 0 {
		t.Fatal("uncommitted task published")
	}
	if _, err := m.Create(input("another")); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal("storage failure did not stop new effects")
	}
}

func TestSessionAncestryAndTaskVisibility(t *testing.T) {
	m, _ := harness(t, 4)
	a := create(t, m, input("a"))
	observe(t, m, Event{TaskID: a.ID, Target: ref("child"), State: Running})
	in := input("b")
	in.From = ref("child")
	in.ParentTaskID = a.ID
	b := create(t, m, in)
	observe(t, m, Event{TaskID: b.ID, Target: ref("grandchild"), State: Running})
	if !m.CanAccess(ref("parent"), b.ID) || !m.CanAccess(ref("child"), b.ID) || !m.CanAccess(ref("grandchild"), b.ID) || m.CanAccess(ref("other"), b.ID) {
		t.Fatal("bad task visibility")
	}
	in = input("c")
	in.From = ref("grandchild")
	in.ParentTaskID = b.ID
	if _, err := m.Create(in); !IsCode(err, "DELEGATION_DEPTH") {
		t.Fatal(err)
	}
	in = input("back")
	in.From = ref("child")
	in.Target = ref("parent")
	if _, err := m.Create(in); !IsCode(err, "DELEGATION_CYCLE") {
		t.Fatal(err)
	}
}
