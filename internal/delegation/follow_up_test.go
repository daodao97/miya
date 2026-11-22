package delegation

import (
	"fmt"
	"testing"
	"time"
)

func TestFollowUpSurvivesRestartAndDeliversOnce(t *testing.T) {
	m, now := harness(t, 4)
	receiver := ref("receiver")
	at := now.Add(time.Minute)
	d, err := m.ScheduleFollowUp(receiver, "item-1", at, "Check the CI run")
	if err != nil {
		t.Fatal(err)
	}
	if receivers := m.PendingReceivers(); len(receivers) != 0 {
		t.Fatalf("timer is eligible before its due time: %v", receivers)
	}
	if err := m.Pause(receiver, true); err != nil {
		t.Fatal(err)
	}
	*now = at
	m, err = Open(NewStore(m.store.dir), Options{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := m.ClaimDelivery(receiver)
	if err != nil || claimed.ID != d.ID || claimed.State != NotifySubmitting {
		t.Fatalf("claim after restart = %+v, error = %v", claimed, err)
	}
	m, err = Open(NewStore(m.store.dir), Options{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	claimed, err = m.ClaimDelivery(receiver)
	if err != nil || claimed.ID != d.ID {
		t.Fatalf("prepared timer not recovered: %+v %v", claimed, err)
	}
	if err := m.BeginDeliveryInput(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeliveryOutcome(d.ID, "turn-1", "accepted", ""); err != nil {
		t.Fatal(err)
	}
	again, err := m.ScheduleFollowUp(receiver, "item-1", at, "Check the CI run")
	if err != nil || again.ID != d.ID || again.State != NotifyDelivered {
		t.Fatalf("retry did not preserve previous outcome: %+v %v", again, err)
	}
	if len(m.FollowUps()) != 0 || len(m.PendingReceivers()) != 0 {
		t.Fatal("delivered timer stayed active")
	}
}

func TestFollowUpUnknownInputIsNeverReplayed(t *testing.T) {
	m, now := harness(t, 4)
	receiver := ref("receiver")
	d, err := m.ScheduleFollowUp(receiver, "item", now.Add(time.Minute), "Check back")
	if err != nil {
		t.Fatal(err)
	}
	*now = d.FollowUpAt
	if claim, err := m.ClaimDelivery(receiver); err != nil || claim.ID != d.ID {
		t.Fatalf("claim = %+v %v", claim, err)
	}
	if err := m.BeginDeliveryInput(d.ID); err != nil {
		t.Fatal(err)
	}
	m, err = Open(NewStore(m.store.dir), Options{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	if claim, err := m.ClaimDelivery(receiver); err != nil || claim.ID != "" {
		t.Fatalf("uncertain input was replayed: %+v %v", claim, err)
	}
	if timers := m.FollowUps(); len(timers) != 1 || timers[0].State != NotifyNeedsAttention {
		t.Fatalf("uncertain input must stay visible: %+v", timers)
	}
	if err := m.CancelFollowUps(ref("someone-else"), d.ID); err == nil {
		t.Fatal("another conversation dismissed the follow-up")
	}
	if err := m.CancelFollowUps(receiver, d.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Delivery(d.ID); len(m.FollowUps()) != 0 || got.Resolution != ResolutionAbandoned {
		t.Fatalf("uncertainty was lost or the timer is still active: %+v", got)
	}
}

func TestFollowUpLimitsAndCancelAll(t *testing.T) {
	m, now := harness(t, 4)
	receiver := ref("receiver")
	for _, delay := range []time.Duration{0, -time.Minute, MaxFollowUpDelay + time.Second} {
		if _, err := m.ScheduleFollowUp(receiver, "invalid", now.Add(delay), "Check back"); err == nil {
			t.Fatalf("accepted an out-of-range delay %v", delay)
		}
	}
	for i := range MaxFollowUps + 1 {
		_, err := m.ScheduleFollowUp(receiver, fmt.Sprint(i), now.Add(time.Minute), "Check back")
		if (err != nil) != (i == MaxFollowUps) {
			t.Fatalf("follow-up %d: %v", i, err)
		}
	}
	if _, err := m.ScheduleFollowUp(ref("other"), "a", now.Add(time.Minute), "Check separately"); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelFollowUps(receiver, ""); err != nil {
		t.Fatal(err)
	}
	if timers := m.FollowUps(); len(timers) != 1 || timers[0].Notify != ref("other") {
		t.Fatalf("cancelled the wrong timers: %+v", timers)
	}
}
