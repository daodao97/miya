package automation

import (
	"path/filepath"
	"testing"
	"time"

	"miya/internal/chat/model"
)

func atLocal(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCronNext(t *testing.T) {
	cases := []struct{ spec, after, want string }{
		{"0 9 * * 1-5", "2026-10-30 09:00", "2026-11-02 09:00"}, // Friday 9:00 to Monday
		{"0 9 * * 1-5", "2026-11-02 08:59", "2026-11-02 09:00"},
		{"*/15 * * * *", "2026-11-01 10:07", "2026-11-01 10:15"},
		{"0 * * * *", "2026-11-01 23:30", "2026-11-02 00:00"},
		{"30 18 1 * *", "2026-11-02 00:00", "2026-12-01 18:30"},
		{"0 10 * * mon", "2026-11-01 12:00", "2026-11-02 10:00"},
		{"0 0 * * 7", "2026-11-02 00:00", "2026-11-08 00:00"}, // 7 is Sunday
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"},
		{"0 12 13 * 5", "2026-11-01 00:00", "2026-11-06 12:00"}, // the 13th or a Friday
	}
	for _, c := range cases {
		s, err := ParseCron(c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got := s.Next(atLocal(c.after)); !got.Equal(atLocal(c.want)) {
			t.Errorf("%s after %s: got %s, want %s", c.spec, c.after, got.Format("2006-01-02 15:04 Mon"), c.want)
		}
	}
}

func TestCronRejectsBadSpecs(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "5-1 * * * *", "*/0 * * * *", "x * * * *"} {
		if _, err := ParseCron(spec); err == nil {
			t.Errorf("%q accepted", spec)
		}
	}
}

func TestDescribeCron(t *testing.T) {
	cases := map[string]string{
		"0 9 * * 1-5":     "Weekdays 9:00",
		"30 18 * * *":     "Daily at 18:30",
		"0 9 * * 0,6":     "Weekends 9:00",
		"0 10 * * 1":      "Weekly: Mon 10:00",
		"0 10 * * 1,3":    "Weekly: Mon, Wed 10:00",
		"0 * * * *":       "Hourly",
		"*/10 * * * *":    "Every 10 minutes",
		"0 */2 * * *":     "Every 2 hours",
		"0 9 1 * *":       "Monthly on day 1 at 9:00",
		"0 9 1 1 *":       "0 9 1 1 *",
		"bad":             "bad",
		"0 9 * * sun":     "Weekly: Sun 9:00",
		"15 * * * *":      "Every hour at minute 15",
		"0 8,12 * * *":    "0 8,12 * * *",
		"0 9 * * 1-5 ":    "Weekdays 9:00",
		"0 9 * * mon-fri": "Weekdays 9:00",
	}
	for spec, want := range cases {
		if got := DescribeCron(spec); got != want {
			t.Errorf("%q: got %q, want %q", spec, got, want)
		}
	}
}

func TestAutomationStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automations.json")
	s := Load(path)
	a := Automation{Name: " 每日 Issue 分拣 ", Project: "/ws", Agent: "claude", Instructions: "看 Issue", Schedule: "0  9 * * 1-5", Check: "go test ./...", Rounds: 9, Enabled: true}
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	if a.Agent != model.AgentClaude || a.Rounds != maxAutomationRounds || a.Schedule != "0 9 * * 1-5" || a.Name != "每日 Issue 分拣" || a.ID == "" {
		t.Fatalf("not normalized: %+v", a)
	}
	noCheck := Automation{Name: "日报", Project: "/ws", Instructions: "写日报", Schedule: "0 18 * * *", Rounds: 3}
	if err := noCheck.Normalize(); err != nil || noCheck.Rounds != 1 || noCheck.Agent != model.AgentCodex {
		t.Fatalf("no-check automation: %+v %v", noCheck, err)
	}
	if bad := (Automation{Name: "x", Project: "/ws", Instructions: "y", Schedule: "0 25 * * *"}); bad.Normalize() == nil {
		t.Fatal("bad schedule accepted")
	}
	s.Automations = append(s.Automations, a, noCheck)
	for i := 0; i < RunHistory+5; i++ {
		s.AddRun(a.ID, Run{Result: Passed, Round: i})
	}
	s.UpdateLastRun(a.ID, func(r *Run) { r.Summary = "最后一次" })
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got := Load(path)
	if len(got.Automations) != 2 || !got.AnyEnabled() {
		t.Fatalf("automations lost: %+v", got.Automations)
	}
	runs := got.Runs[a.ID]
	if len(runs) != RunHistory || runs[0].Round != 5 {
		t.Fatalf("history not bounded: %d runs, first round %d", len(runs), runs[0].Round)
	}
	if last, ok := got.LastRun(a.ID); !ok || last.Summary != "最后一次" {
		t.Fatalf("last run: %+v", last)
	}
	if i, ok := got.Find(noCheck.ID); !ok || i != 1 {
		t.Fatal("find failed")
	}
}
