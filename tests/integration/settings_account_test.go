package integration

import (
	"encoding/json"
	"testing"

	"miya/internal/account"
)

func TestAccountQuotaResponse(t *testing.T) {
	var data account.Limits
	err := json.Unmarshal([]byte(`{"rateLimits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1800000000}},"rateLimitsByLimitId":{"codex":{"limitName":"Codex","primary":{"usedPercent":25,"windowDurationMins":300},"secondary":{"usedPercent":80,"windowDurationMins":10080},"credits":{"hasCredits":true,"unlimited":false,"balance":"12.5"}}}}`), &data)
	if err != nil {
		t.Fatal(err)
	}
	q := data.ByID["codex"]
	if q.Primary.UsedPercent != 25 || account.QuotaLabel(q.Primary, 0) != "5-hour quota" || account.QuotaLabel(q.Secondary, 1) != "Weekly quota" || *q.Credits.Balance != "12.5" {
		t.Fatalf("incorrect quota mapping: %+v", q)
	}
	states := []account.Display{
		{Loading: true}, {Error: "error"}, {},
		{Identity: &account.Identity{Type: "apiKey"}},
		{Identity: &account.Identity{Type: "chatgpt", Email: "test@example.com", PlanType: "plus"}, Limits: data},
		{Identity: &account.Identity{Type: "chatgpt"}, UsageError: "unavailable"},
		{Identity: &account.Identity{Type: "chatgpt"}},
	}
	_ = states
}

func TestUnknownQuotaWindowIsNotInvented(t *testing.T) {
	if account.QuotaLabel(&account.QuotaWindow{}, 0) != "Primary quota" {
		t.Fatal("unknown duration presented as a known window")
	}
}
