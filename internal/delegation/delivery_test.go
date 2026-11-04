package delegation

import (
	"testing"
	"time"
)

func TestNotificationsCoalesceButNeverReplayUncertainSubmissions(t *testing.T) {
	m, _ := harness(t, 4)
	in := input("one")
	in.Notify = true
	a := create(t, m, in)
	in.RequestID = "two"
	b := create(t, m, in)
	observe(t, m, Event{TaskID: a.ID, State: Completed, Result: Result{Text: "a"}})
	observe(t, m, Event{TaskID: b.ID, State: Failed})
	if len(m.View().Deliveries) != 1 {
		t.Fatal("pending results not coalesced")
	}
	if err := m.Pause(in.From, true); err != nil {
		t.Fatal(err)
	}
	d, err := m.ClaimDelivery(in.From)
	if err != nil || d.ID != "" {
		t.Fatalf("paused conversation woke: %+v %v", d, err)
	}
	if err := m.Pause(in.From, false); err != nil {
		t.Fatal(err)
	}
	d, err = m.ClaimDelivery(in.From)
	if err != nil || len(d.TaskIDs) != 2 {
		t.Fatalf("claim: %+v %v", d, err)
	}
	if err := m.DeliveryOutcome(d.ID, "", "unknown", "start acknowledgement timed out"); err != nil {
		t.Fatal(err)
	}
	in.RequestID = "third"
	c := create(t, m, in)
	observe(t, m, Event{TaskID: c.ID, State: Completed})
	if next, err := m.ClaimDelivery(in.From); err != nil || next.ID != "" {
		t.Fatal("uncertain notification allowed duplicate wakeup")
	}
	if err := m.DeliveryOutcome(d.ID, "turn-1", "accepted", ""); err != nil {
		t.Fatal(err)
	}
	next, err := m.ClaimDelivery(in.From)
	if err != nil || len(next.TaskIDs) != 1 || next.TaskIDs[0] != c.ID {
		t.Fatalf("later result joined submitted batch: %+v %v", next, err)
	}
}

func TestOnlyProvenPreStartRefusalRetriesAndExpiryIsPerTask(t *testing.T) {
	m, now := harness(t, 4)
	in := input("old")
	in.Notify = true
	a := create(t, m, in)
	observe(t, m, Event{TaskID: a.ID, State: Completed})
	*now = now.Add(DeliveryTTL - time.Minute)
	in.RequestID = "new"
	b := create(t, m, in)
	observe(t, m, Event{TaskID: b.ID, State: Completed})
	*now = now.Add(2 * time.Minute)
	d, err := m.ClaimDelivery(in.From)
	if err != nil || len(d.TaskIDs) != 1 || d.TaskIDs[0] != b.ID {
		t.Fatalf("expiry removed wrong task: %+v %v", d, err)
	}
	if err := m.DeliveryOutcome(d.ID, "", "not_started", "busy before start"); err != nil {
		t.Fatal(err)
	}
	if retry, err := m.ClaimDelivery(in.From); err != nil || retry.ID != "" {
		t.Fatal("pre-start refusal retried without backoff")
	}
	*now = now.Add(time.Second)
	retry, err := m.ClaimDelivery(in.From)
	if err != nil || retry.ID != d.ID {
		t.Fatal("proven refusal did not retry same notification")
	}
	if err := m.Unwatch(in.From, b.ID); !IsCode(err, "ALREADY_SUBMITTED") {
		t.Fatal(err)
	}
}

func TestExpiryRunsWhilePausedAndNeverExpiresUncertainSubmission(t *testing.T) {
	m, now := harness(t, 4)
	in := input("old-paused")
	in.Notify = true
	old := create(t, m, in)
	observe(t, m, Event{TaskID: old.ID, State: Completed})
	*now = now.Add(time.Hour)
	in.RequestID = "new-paused"
	newer := create(t, m, in)
	observe(t, m, Event{TaskID: newer.ID, State: Completed})
	if err := m.Pause(in.From, true); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	if err := m.ExpireDeliveries(); err != nil {
		t.Fatal(err)
	}
	snapshot := m.View()
	if snapshot.Watches[watchKey(old.ID, in.From)].State != "expired" || snapshot.Watches[watchKey(newer.ID, in.From)].State != "pending" {
		t.Fatal("paused deadline did not advance per item")
	}
	if err := m.Pause(in.From, false); err != nil {
		t.Fatal(err)
	}
	d, err := m.ClaimDelivery(in.From)
	if err != nil || len(d.TaskIDs) != 1 || d.TaskIDs[0] != newer.ID {
		t.Fatalf("claim: %+v %v", d, err)
	}
	if err := m.DeliveryOutcome(d.ID, "", "unknown", "lost ack"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(24 * time.Hour)
	if err := m.ExpireDeliveries(); err != nil {
		t.Fatal(err)
	}
	if m.View().Deliveries[d.ID].State != "needsAttention" {
		t.Fatal("unknown submission expired into retryable work")
	}
}

func TestPreStartBackoffSurvivesRestartAndRespectsDeadline(t *testing.T) {
	m, now := harness(t, 4)
	in := input("retry")
	in.Notify = true
	task := create(t, m, in)
	observe(t, m, Event{TaskID: task.ID, State: Completed})
	for _, delay := range []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, time.Minute, time.Minute} {
		d, err := m.ClaimDelivery(in.From)
		if err != nil || d.ID == "" {
			t.Fatalf("claim: %+v %v", d, err)
		}
		if err := m.DeliveryOutcome(d.ID, "", "not_started", "refused"); err != nil {
			t.Fatal(err)
		}
		m, err = Open(NewStore(m.store.dir), Options{Now: func() time.Time { return *now }})
		if err != nil {
			t.Fatal(err)
		}
		if d, err := m.ClaimDelivery(in.From); err != nil || d.ID != "" {
			t.Fatal("restart reset retry deadline")
		}
		*now = now.Add(delay)
	}
	*now = now.Add(DeliveryTTL)
	if d, err := m.ClaimDelivery(in.From); err != nil || d.ID != "" {
		t.Fatal("retried after notification expiry")
	}
}

func TestWatchTerminalDoesNotGenerateAnotherNotification(t *testing.T) {
	m, _ := harness(t, 4)
	task := create(t, m, input("no-watch"))
	observe(t, m, Event{TaskID: task.ID, State: Completed})
	state, err := m.Watch(task.From, task.ID)
	if err != nil || state != "already_terminal" || len(m.View().Deliveries) != 0 {
		t.Fatalf("terminal watch: %s %v", state, err)
	}
}

func TestAbandonRemovesOnlyItsUnsubmittedNotificationItem(t *testing.T) {
	m, _ := harness(t, 4)
	in := input("a")
	in.Notify = true
	a := create(t, m, in)
	in.RequestID = "b"
	b := create(t, m, in)
	observe(t, m, Event{TaskID: a.ID, State: NeedsAttention})
	if err := m.Abandon(in.From, a.ID, "unknown native execution"); err != nil {
		t.Fatal(err)
	}
	observe(t, m, Event{TaskID: b.ID, State: Completed})
	observe(t, m, Event{TaskID: a.ID, State: Completed})
	d, err := m.ClaimDelivery(in.From)
	if err != nil || len(d.TaskIDs) != 1 || d.TaskIDs[0] != b.ID {
		t.Fatalf("abandoned task restored notification: %+v %v", d, err)
	}
}
