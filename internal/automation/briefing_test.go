package automation

import (
	"strings"
	"testing"
	"time"
)

func (w *wakeHarness) briefings() []string {
	var bodies []string
	for _, n := range w.h.notices {
		if strings.HasPrefix(n.ID, "miya-briefing-") {
			bodies = append(bodies, n.Body)
		}
	}
	return bodies
}

func TestDailyBriefingSumsTheDayUpOnce(t *testing.T) {
	w := newWakeHarness(t) // 2026-11-10 10:00
	if _, err := w.rn.SetBriefing("9"); err != nil || w.rn.Store.Briefing != "09:00" {
		t.Fatalf("set: %q %v", w.rn.Store.Briefing, err)
	}
	// A time already past today waits for tomorrow.
	w.rn.Tick()
	if len(w.briefings()) != 0 {
		t.Fatal("briefed at once for a time already past")
	}
	a := w.add(ingressAutomation(WakePolicy{}))
	w.runIngress(a.ID, "ping", "#12 stale")
	w.now = w.now.Add(time.Minute)
	w.runIngress(a.ID, "ping", "#12 stale")
	w.rn.Store.Stalled = map[string]time.Time{a.ID: w.now}
	w.now = atLocal("2026-11-11 08:59")
	w.rn.Tick()
	if len(w.briefings()) != 0 {
		t.Fatal("briefed before its time")
	}
	w.now = atLocal("2026-11-11 09:00")
	w.rn.Tick()
	w.now = w.now.Add(time.Hour)
	w.rn.Tick()
	got := w.briefings()
	if len(got) != 1 {
		t.Fatalf("briefings: %q", got)
	}
	for _, want := range []string{"2 runs", "2 passed, 0 failed", "1 found nothing new", "Needs you:", a.Name + " · Stalled"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("briefing lacks %q:\n%s", want, got[0])
		}
	}
	// A day with nothing to say sends nothing.
	delete(w.rn.Store.Stalled, a.ID)
	w.now = atLocal("2026-11-12 09:30")
	w.rn.Tick()
	if len(w.briefings()) != 1 || !w.rn.Store.BriefedAt.Equal(w.now) {
		t.Fatalf("an empty day: %q, briefed at %v", w.briefings(), w.rn.Store.BriefedAt)
	}
	// Off sends none.
	if _, err := w.rn.SetBriefing(""); err != nil {
		t.Fatal(err)
	}
	w.runIngress(a.ID, "ping", "#20 new")
	w.now = atLocal("2026-11-13 09:30")
	w.rn.Tick()
	if len(w.briefings()) != 1 {
		t.Fatal("briefed while off")
	}
	if _, err := w.rn.SetBriefing("25:99"); err == nil {
		t.Fatal("a bad time was accepted")
	}
}
