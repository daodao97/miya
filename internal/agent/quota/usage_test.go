package quota

import (
	"math"
	"testing"
	"time"

	"miya/internal/agent/account"
)

func TestCodexResetSelectionUsesSoonestAvailableCard(t *testing.T) {
	past, soon, later := time.Now().Add(-time.Hour).Unix(), time.Now().Add(time.Hour).Unix(), time.Now().Add(24*time.Hour).Unix()
	r := Reading{Agent: "codex", AccountKey: "id:fixture", Home: t.TempDir(), Resets: &account.ResetCreditsSummary{AvailableCount: 3, Credits: []account.ResetCredit{
		{ID: "never", ResetType: "codexRateLimits", Status: "available"},
		{ID: "late", ResetType: "codexRateLimits", Status: "available", ExpiresAt: &later},
		{ID: "first", ResetType: "codexRateLimits", Status: "available", ExpiresAt: &soon},
		{ID: "expired", ResetType: "codexRateLimits", Status: "available", ExpiresAt: &past},
		{ID: "used", ResetType: "codexRateLimits", Status: "redeemed", ExpiresAt: &soon},
		{ID: "unknown", ResetType: "unknown", Status: "available"},
	}}}
	r.Resets.Normalize(time.Now())
	a, err := NewResetAttempt(r)
	if err != nil || a.CreditID != "first" || len(a.Key) != 36 || a.AccountKey != r.AccountKey {
		t.Fatalf("wrong selection: %+v %v", a, err)
	}
	r.Resets.AvailableCount = 4
	a, err = NewResetAttempt(r)
	if err != nil || a.CreditID != "" {
		t.Fatalf("partial list claims earliest selection: %+v %v", a, err)
	}
	r.AccountKey = ""
	if _, err = NewResetAttempt(r); err == nil {
		t.Fatal("unidentified account can consume a card")
	}
}

func TestPercentBounds(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{{-3, 0}, {0, 0}, {120, 100}, {math.NaN(), 0}, {math.Inf(1), 0}} {
		if got := Percent(tc.in); got != tc.want {
			t.Fatalf("%v: %v", tc.in, got)
		}
	}
}
