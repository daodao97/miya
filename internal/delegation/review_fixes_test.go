package delegation

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecoveryOnlyReleasesProvenPreInputTasks(t *testing.T) {
	for _, tc := range []struct {
		name, agent, phase string
		existing           bool
		want               State
	}{
		{"new codex", "codex", "prepared", false, Failed},
		{"new claude", "claude-code", "prepared", false, Failed},
		{"existing codex", "codex", "prepared", true, Failed},
		{"legacy unbound codex", "codex", "", false, Failed},
		{"legacy claude", "claude-code", "", true, NeedsAttention},
		{"possibly written", "codex", "submitting", true, NeedsAttention},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := harness(t, 1)
			in := input("recovery")
			in.Agent, in.Notify = tc.agent, true
			if tc.existing {
				in.Target = strings.Replace(ref("target"), "/codex/", "/"+tc.agent+"/", 1)
			}
			task := create(t, m, in)
			if err := m.change(func(s *Snapshot) error {
				v := s.Tasks[task.ID]
				v.SubmissionPhase = SubmissionPhase(tc.phase)
				s.Tasks[v.ID] = v
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			m, err := Open(NewStore(m.store.dir), Options{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Recover(); err != nil {
				t.Fatal(err)
			}
			got, _ := m.Read(task.ID)
			if got.State != tc.want {
				t.Fatalf("got %+v", got)
			}
			if (occupied(m.View()) == 0) != Terminal(tc.want) {
				t.Fatal("wrong slot release")
			}
			if Terminal(tc.want) && len(m.View().Deliveries) != 1 {
				t.Fatal("pre-input failure did not notify its parent")
			}
		})
	}
}

func TestRecoveredDeliveryCanBeAbandonedWithoutReplayingIt(t *testing.T) {
	m, _ := harness(t, 4)
	in := input("first")
	in.Notify = true
	first := create(t, m, in)
	observe(t, m, Event{TaskID: first.ID, State: Completed})
	d, err := m.ClaimDelivery(in.From)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.BeginDeliveryInput(d.ID); err != nil {
		t.Fatal(err)
	}
	m, err = Open(NewStore(m.store.dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Recover(); err != nil {
		t.Fatal(err)
	}
	in.RequestID = "second"
	second := create(t, m, in)
	observe(t, m, Event{TaskID: second.ID, State: Completed})
	if next, err := m.ClaimDelivery(in.From); err != nil || next.ID != "" {
		t.Fatal("unknown submission did not block", err)
	}
	op := Operation{Caller: in.From, RequestID: "abandon-delivery", Name: "abandon_delivery", TaskID: first.ID, DeliveryID: d.ID, Reason: "user verified the old notification cannot be reconciled"}
	wrong := op
	wrong.Caller = ref("other")
	if _, _, err := m.Mutate(wrong); !IsCode(err, "ACCESS_DENIED") {
		t.Fatal("resolution crossed receiver", err)
	}
	if _, fresh, err := m.Mutate(op); err != nil || !fresh {
		t.Fatal(err)
	}
	if old, _ := m.Delivery(d.ID); old.State != "needsAttention" || old.Resolution != "abandoned" {
		t.Fatal("resolution fabricated native outcome", old)
	}
	next, err := m.ClaimDelivery(in.From)
	if err != nil || len(next.TaskIDs) != 1 || next.TaskIDs[0] != second.ID {
		t.Fatal("later notification did not unblock", next, err)
	}
	if err := m.DeliveryOutcome(d.ID, "", "not_started", "late refusal"); err != nil {
		t.Fatal(err)
	}
	if old, _ := m.Delivery(d.ID); old.State == "pending" {
		t.Fatal("late callback replayed abandoned notification")
	}
	if err := m.DeliveryOutcome(d.ID, "original-turn", "accepted", ""); err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := m.Mutate(op); err != nil || fresh {
		t.Fatal("old receipt changed", err)
	}
	if m.View().Watches[watchKey(first.ID, in.From)].State != "cancelled" {
		t.Fatal("late callback revived old observation")
	}
}

func TestRecoveryRetriesOnlyDeliveryWhoseInputIntentWasNotCommitted(t *testing.T) {
	for _, phase := range []string{"prepared", "submitting", ""} {
		t.Run(phase, func(t *testing.T) {
			m, _ := harness(t, 4)
			in := input("notify")
			in.Notify = true
			task := create(t, m, in)
			observe(t, m, Event{TaskID: task.ID, State: Completed})
			d, err := m.ClaimDelivery(in.From)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.change(func(s *Snapshot) error {
				v := s.Deliveries[d.ID]
				v.SubmissionPhase = SubmissionPhase(phase)
				s.Deliveries[d.ID] = v
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err = m.Recover(); err != nil {
				t.Fatal(err)
			}
			got, _ := m.Delivery(d.ID)
			if (got.State == "pending") != (phase == "prepared") {
				t.Fatalf("unsafe recovery %+v", got)
			}
		})
	}
}

func TestIdleSchedulingAtRetentionLimitDoesNotCloneTaskHistory(t *testing.T) {
	m, now := harness(t, 4)
	base := create(t, m, input("base"))
	observe(t, m, Event{TaskID: base.ID, State: Completed})
	base, _ = m.Read(base.ID)
	if err := m.change(func(s *Snapshot) error {
		for i := 1; i < 10000; i++ {
			q := base
			q.ID = fmt.Sprintf("task-%032x", i)
			q.RequestID = fmt.Sprint(i)
			q.RequestHash = requestHash(input(q.RequestID))
			s.Tasks[q.ID] = q
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	revision := m.Revision()
	start := time.Now()
	allocs := testing.AllocsPerRun(20, func() {
		if err := m.ExpireDeliveries(); err != nil {
			panic(err)
		}
		if tasks, err := m.ClaimQueued(); err != nil || len(tasks) != 0 {
			panic("unexpected scheduling")
		}
		if m.HasActiveExecutions() || len(m.PendingReceivers()) != 0 {
			panic("idle history treated as active")
		}
	})
	if allocs > 0 || m.Revision() != revision {
		t.Fatalf("idle scheduling cloned/changed history: allocs=%v", allocs)
	}
	t.Logf("10000 retained tasks: idle scheduling %.0f allocations, %s for 21 passes at %s", allocs, time.Since(start), now.Format(time.RFC3339))
}

func TestProgressCheckpointsCommitInOneTransaction(t *testing.T) {
	m, _ := harness(t, 4)
	var events []Event
	for i := 0; i < 2; i++ {
		q := create(t, m, input(fmt.Sprint(i)))
		target := ref(fmt.Sprint("target", i))
		observe(t, m, Event{TaskID: q.ID, Target: target, NativeTurnID: "turn", State: Running})
		q, _ = m.Read(q.ID)
		events = append(events, Event{TaskID: q.ID, Target: target, NativeTurnID: "turn", State: Running, Progress: "latest", Sequence: 1, ExpectedRevision: q.Revision})
	}
	before := m.Revision()
	if err := m.ObserveProgress(events); err != nil {
		t.Fatal(err)
	}
	if m.Revision() != before+1 {
		t.Fatal("progress was not batched")
	}
	for _, e := range events {
		q, _ := m.Read(e.TaskID)
		if q.Progress != "latest" {
			t.Fatal(q)
		}
	}
	if err := m.ObserveProgress(events); err != nil || m.Revision() != before+1 {
		t.Fatal("stale progress rewrote history", err)
	}
}
