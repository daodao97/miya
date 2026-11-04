package delegation

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestOperationReceiptDoesNotReviveUnwatchedTaskAfterRestart(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("create"))
	watch := Operation{Caller: task.From, RequestID: "watch", Name: "watch_task", TaskID: task.ID}
	original, fresh, err := m.Mutate(watch)
	if err != nil || !fresh || original.Notification != "watching" {
		t.Fatalf("watch: %+v %v", original, err)
	}
	unwatch := watch
	unwatch.Name, unwatch.RequestID = "unwatch_task", "unwatch"
	if _, fresh, err := m.Mutate(unwatch); err != nil || !fresh {
		t.Fatal(err)
	}
	m, err = Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	retry, fresh, err := m.Mutate(watch)
	if err != nil || fresh || !reflect.DeepEqual(original, retry) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	observe(t, m, Event{TaskID: task.ID, State: Completed})
	if len(m.View().Deliveries) != 0 || m.View().Watches[watchKey(task.ID, task.From)].State != "cancelled" {
		t.Fatal("old watch retry reactivated cancelled observation")
	}
}

func TestRequestIDConflictsAcrossOperationsAndCreation(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("create"))
	op := Operation{Caller: task.From, RequestID: "mutate", Name: "watch_task", TaskID: task.ID}
	if _, _, err := m.Mutate(op); err != nil {
		t.Fatal(err)
	}
	op.Name = "cancel_task"
	if _, _, err := m.Mutate(op); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatal(err)
	}
	op.RequestID = "create"
	if _, _, err := m.Mutate(op); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatal(err)
	}
	if _, err := m.Create(input("mutate")); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatal(err)
	}
	got, _ := m.Read(task.ID)
	if got.CancelRequested {
		t.Fatal("conflicting operation mutated task")
	}
}

func TestOperationWriteFailurePublishesNeitherIntentNorReceipt(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("create"))
	m.store.beforeIndex = func() error { return errors.New("disk unavailable") }
	op := Operation{Caller: task.From, RequestID: "cancel", Name: "cancel_task", TaskID: task.ID}
	if _, fresh, err := m.Mutate(op); fresh || !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	got, _ := m.Read(task.ID)
	if got.CancelRequested || len(m.View().Operations) != 0 || m.StorageError() == "" {
		t.Fatal("uncommitted intent/receipt escaped or storage fault was hidden")
	}
	restarted, err := Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := restarted.Read(task.ID); got.CancelRequested || len(restarted.View().Operations) != 0 {
		t.Fatal("orphan task revision was mistaken for committed cancellation")
	}
}

func TestStaleRecheckCannotReopenDisconnectedExecution(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("create"))
	observe(t, m, Event{TaskID: task.ID, Target: ref("target"), NativeTurnID: "turn", State: Running})
	before, _ := m.Read(task.ID)
	observe(t, m, Event{TaskID: task.ID, State: NeedsAttention})
	if err := m.Observe(Event{TaskID: task.ID, State: Running, ExpectedRevision: before.Revision}); !IsCode(err, "STALE_EVENT") {
		t.Fatal(err)
	}
	if got, _ := m.Read(task.ID); got.State != NeedsAttention {
		t.Fatal("stale live evidence reopened unknown execution")
	}
}

func TestNativeTargetCannotChangeTaskBackend(t *testing.T) {
	m, _ := harness(t, 4)
	wrong := strings.Replace(ref("target"), "/codex/", "/claude-code/", 1)
	in := input("wrong-existing")
	in.Target = wrong
	if _, err := m.Create(in); !IsCode(err, "INVALID_ARGUMENT") {
		t.Fatal("mismatched target backend accepted", err)
	}
	task := create(t, m, input("new"))
	if err := m.Observe(Event{TaskID: task.ID, Target: wrong, State: Running}); !IsCode(err, "INVALID_ARGUMENT") {
		t.Fatal("native bind changed backend", err)
	}
	if got, _ := m.Read(task.ID); got.Target != "" {
		t.Fatal("invalid binding escaped transaction")
	}
}

func TestCancellingParentRejectsNewChildrenButKeepsAcceptedChildren(t *testing.T) {
	m, _ := harness(t, 4)
	parent := create(t, m, input("parent"))
	observe(t, m, Event{TaskID: parent.ID, Target: ref("child-session"), State: Running})
	in := input("accepted-child")
	in.From, in.ParentTaskID = ref("child-session"), parent.ID
	child := create(t, m, in)
	if _, err := m.Cancel(parent.From, parent.ID); err != nil {
		t.Fatal(err)
	}
	if retry := create(t, m, in); retry.ID != child.ID {
		t.Fatal("parent cancellation invalidated an accepted child's retry")
	}
	in.RequestID = "late-child"
	if _, err := m.Create(in); !IsCode(err, "PARENT_STOPPING") {
		t.Fatal("cancelled parent spawned more work", err)
	}
	if got, _ := m.Read(child.ID); got.CancelRequested {
		t.Fatal("cancelling parent cancelled already independent children")
	}
}

func TestCancelAllKeepsItsOriginalScopeAcrossRetryAndRestart(t *testing.T) {
	m, _ := harness(t, 4)
	parent := create(t, m, input("parent"))
	observe(t, m, Event{TaskID: parent.ID, Target: ref("child-session"), State: Running})
	in := input("nested")
	in.From = ref("child-session")
	in.ParentTaskID = parent.ID
	child := create(t, m, in)
	in = input("other")
	in.From = ref("unrelated")
	other := create(t, m, in)
	op := Operation{Caller: parent.From, RequestID: "stop-all", Name: "cancel_all"}
	receipt, fresh, err := m.Mutate(op)
	if err != nil || !fresh || len(receipt.TaskIDs) != 2 {
		t.Fatalf("batch: %+v %v", receipt, err)
	}
	for _, id := range []string{parent.ID, child.ID} {
		if task, _ := m.Read(id); !task.CancelRequested {
			t.Fatal("accepted scope not cancelled", id)
		}
	}
	if task, _ := m.Read(other.ID); task.CancelRequested {
		t.Fatal("batch crossed conversation ancestry")
	}
	later := create(t, m, input("later"))
	mutated := receipt
	mutated.TaskIDs[0] = "corrupted"
	m, err = Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	retry, fresh, err := m.Mutate(op)
	if err != nil || fresh || len(retry.TaskIDs) != 2 || retry.TaskIDs[0] == "corrupted" {
		t.Fatalf("batch receipt: %+v %v", retry, err)
	}
	if task, _ := m.Read(later.ID); task.CancelRequested {
		t.Fatal("retry cancelled a task created after the original operation")
	}
}

func TestCancelAllWriteFailureDoesNotPublishPartialCancellation(t *testing.T) {
	m, _ := harness(t, 4)
	a := create(t, m, input("a"))
	b := create(t, m, input("b"))
	m.store.beforeIndex = func() error { return errors.New("injected batch failure") }
	if _, fresh, err := m.Mutate(Operation{Caller: a.From, RequestID: "stop-all", Name: "cancel_all"}); fresh || !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if task, _ := m.Read(id); task.CancelRequested {
			t.Fatal("partial cancellation published")
		}
	}
}

func TestCommittedRetriesRemainReadableDuringLaterStorageFailure(t *testing.T) {
	m, _ := harness(t, 4)
	in := input("accepted")
	task := create(t, m, in)
	op := Operation{Caller: task.From, RequestID: "watch", Name: "watch_task", TaskID: task.ID}
	receipt, _, err := m.Mutate(op)
	if err != nil {
		t.Fatal(err)
	}
	m.store.beforeIndex = func() error { return errors.New("later IO fault") }
	if _, err := m.Cancel(task.From, task.ID); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	if retry, err := m.Create(in); err != nil || retry.ID != task.ID {
		t.Fatal("committed creation hidden by write fault", err)
	}
	if retry, fresh, err := m.Mutate(op); err != nil || fresh || !reflect.DeepEqual(receipt, retry) {
		t.Fatal("committed receipt hidden by write fault", err)
	}
	if _, err := m.Create(input("new")); !IsCode(err, "STORAGE_UNAVAILABLE") {
		t.Fatal("new work admitted during write failure", err)
	}
}
