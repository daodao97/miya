package notification

import (
	"fmt"
	"testing"
	"time"
)

const (
	phoneA = "0123456789abcdef0123456789abcdef"
	phoneB = "fedcba9876543210fedcba9876543210"
)

func policyHub(t *testing.T, dir string, ids ...string) *Hub {
	t.Helper()
	if len(ids) == 0 {
		ids = []string{phoneA}
	}
	var regs []Registration
	for _, id := range ids {
		regs = append(regs, Registration{ID: id, Token: "aa" + id, Updated: time.Now()})
	}
	h := NewQueueHub(dir, regs...)
	h.clock = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local) }
	return h
}

func drain(h *Hub) []pushDelivery {
	var out []pushDelivery
	for {
		select {
		case d := <-h.queue:
			out = append(out, d)
		default:
			return out
		}
	}
}

var noticeSeq int

// send publishes a notice of category and reports whether it alerted.
func send(t *testing.T, h *Hub, category string, urgent bool) bool {
	t.Helper()
	drain(h)
	noticeSeq++
	h.Publish(Notice{ID: fmt.Sprintf("n-%d", noticeSeq), Thread: "1", Title: "Miya · Completed", Body: "done", Category: category, Topic: "repo", Urgent: urgent})
	ds := drain(h)
	if len(ds) == 0 {
		t.Fatal("nothing delivered")
	}
	n, ok := h.notificationFor(ds[0])
	if !ok {
		t.Fatal("delivery dropped")
	}
	return !n.Payload.Silent
}

func dismiss(h *Hub, id string) { h.MarkAllRead(id, h.State(id).Revision) }
func open(h *Hub, id string)    { h.MarkRead(id, "thread:1", h.State(id).Revision) }

func TestDismissalsTeachACategoryToArriveAsABadge(t *testing.T) {
	h := policyHub(t, t.TempDir())
	const cat = "completion:/work/repo"
	for i := range LearnAfter {
		if !send(t, h, cat, false) {
			t.Fatalf("notice %d did not alert", i)
		}
		dismiss(h, phoneA)
	}
	if send(t, h, cat, false) {
		t.Fatal("dismissed category still alerts")
	}
	if !send(t, h, "completion:/work/other", false) {
		t.Fatal("rule quieted another project")
	}
	if !send(t, h, cat, true) {
		t.Fatal("rule quieted an urgent notice")
	}
	rules := h.Policy().Rules
	if len(rules) != 1 || rules[0].Category != cat || rules[0].Kind != "completion" || rules[0].Name != "repo" || rules[0].LearnedAt.IsZero() {
		t.Fatalf("rules: %+v", rules)
	}
}

func TestOpeningEndsADismissalStreak(t *testing.T) {
	h := policyHub(t, t.TempDir())
	const cat = "automation:a1"
	send(t, h, cat, false)
	dismiss(h, phoneA)
	send(t, h, cat, false)
	open(h, phoneA)
	send(t, h, cat, false)
	dismiss(h, phoneA)
	if !send(t, h, cat, false) || len(h.Policy().Rules) != 0 {
		t.Fatal("an opened notice did not reset the count")
	}
}

func TestUrgentNoticesNeverLearn(t *testing.T) {
	h := policyHub(t, t.TempDir())
	for range LearnAfter + 1 {
		if !send(t, h, "automation:a1", true) {
			t.Fatal("urgent notice quieted")
		}
		dismiss(h, phoneA)
	}
	if len(h.Policy().Rules) != 0 {
		t.Fatal("urgent notices made a rule")
	}
}

func TestOpeningQuietNoticesLiftsTheRule(t *testing.T) {
	h := policyHub(t, t.TempDir())
	const cat = "briefing"
	for range LearnAfter {
		send(t, h, cat, false)
		dismiss(h, phoneA)
	}
	send(t, h, cat, false)
	open(h, phoneA)
	send(t, h, cat, false)
	dismiss(h, phoneA)
	if send(t, h, cat, false) {
		t.Fatal("a dismissed badge did not reset the open count")
	}
	open(h, phoneA)
	if send(t, h, cat, false) {
		t.Fatal("rule lifted after one open")
	}
	open(h, phoneA)
	if !send(t, h, cat, false) || len(h.Policy().Rules) != 0 {
		t.Fatal("opening quiet notices did not lift the rule")
	}
}

func TestOneNoticeCountsOnceAcrossPhones(t *testing.T) {
	h := policyHub(t, t.TempDir(), phoneA, phoneB)
	send(t, h, "webhook", false)
	dismiss(h, phoneA)
	dismiss(h, phoneB)
	if !send(t, h, "webhook", false) {
		t.Fatal("one notice dismissed on two phones counted twice")
	}
}

func TestQuietHoursSendBadgesAndTeachNothing(t *testing.T) {
	h := policyHub(t, t.TempDir())
	if err := h.SetQuietHours(QuietHours{Enabled: true, Start: "22:00", End: "08:00"}); err != nil {
		t.Fatal(err)
	}
	h.clock = func() time.Time { return time.Date(2026, 11, 20, 23, 30, 0, 0, time.Local) }
	for range LearnAfter {
		if send(t, h, "webhook", false) {
			t.Fatal("alerted in quiet hours")
		}
		if send(t, h, "webhook", true) {
			t.Fatal("urgent notice alerted in quiet hours")
		}
		dismiss(h, phoneA)
	}
	h.clock = func() time.Time { return time.Date(2026, 11, 21, 8, 0, 0, 0, time.Local) }
	if !send(t, h, "webhook", false) || len(h.Policy().Rules) != 0 {
		t.Fatal("quiet hours taught a rule")
	}
}

func TestReadingOnTheDesktopIsNotADismissal(t *testing.T) {
	h := policyHub(t, t.TempDir())
	for range LearnAfter {
		send(t, h, "webhook", false)
		h.NoteOpenedElsewhere((Notice{Thread: "1"}).UnreadKey())
		dismiss(h, phoneA)
	}
	if len(h.Policy().Rules) != 0 {
		t.Fatal("clearing a badge read on the desktop counted as a dismissal")
	}
}

func TestRulesPersistAndCanBeForgotten(t *testing.T) {
	dir := t.TempDir()
	h := policyHub(t, dir)
	for range LearnAfter {
		send(t, h, "probe:a1", false)
		dismiss(h, phoneA)
	}
	if err := h.SetQuietHours(QuietHours{Enabled: true, Start: "23:00", End: "07:00"}); err != nil {
		t.Fatal(err)
	}
	reopened := policyHub(t, dir)
	view := reopened.Policy()
	if len(view.Rules) != 1 || !view.Quiet.Enabled || view.Quiet.Start != "23:00" {
		t.Fatalf("not persisted: %+v", view)
	}
	if err := reopened.ForgetRule("probe:a1"); err != nil {
		t.Fatal(err)
	}
	if !send(t, reopened, "probe:a1", false) {
		t.Fatal("forgotten rule still quiets")
	}
	if err := reopened.ForgetRule("missing"); err == nil {
		t.Fatal("forgot an unknown rule")
	}
}

func TestQuietHoursWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.Local) }
	night := QuietHours{Enabled: true, Start: "22:00", End: "08:00"}
	for _, c := range []struct {
		q     QuietHours
		at    time.Time
		quiet bool
	}{
		{night, at(22, 0), true}, {night, at(3, 0), true}, {night, at(8, 0), false}, {night, at(12, 0), false},
		{QuietHours{Enabled: true, Start: "12:00", End: "13:00"}, at(12, 30), true},
		{QuietHours{Enabled: true, Start: "12:00", End: "13:00"}, at(13, 0), false},
		{QuietHours{Enabled: true, Start: "09:00", End: "09:00"}, at(9, 0), false},
		{QuietHours{Start: "22:00", End: "08:00"}, at(23, 0), false},
	} {
		if got := c.q.Quiet(c.at); got != c.quiet {
			t.Fatalf("%+v at %s: %v", c.q, c.at.Format("15:04"), got)
		}
	}
	h := policyHub(t, t.TempDir())
	for _, bad := range []QuietHours{{Start: "25:00", End: "08:00"}, {Start: "9:00", End: "08:00"}, {Start: "22:00"}} {
		if err := h.SetQuietHours(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
