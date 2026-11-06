package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	testing "testing"
)

func TestAccountQuotaResponse(t *testing.T) {
	var data appmodel.AccountLimits
	err := json.Unmarshal([]byte(`{"rateLimits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1800000000}},"rateLimitsByLimitId":{"codex":{"limitName":"Codex","primary":{"usedPercent":25,"windowDurationMins":300},"secondary":{"usedPercent":80,"windowDurationMins":10080},"credits":{"hasCredits":true,"unlimited":false,"balance":"12.5"}}}}`), &data)
	if err != nil {
		t.Fatal(err)
	}
	q := data.ByID["codex"]
	if q.Primary.UsedPercent != 25 || appmodel.QuotaLabel(q.Primary, 0) != "5-hour quota" || appmodel.QuotaLabel(q.Secondary, 1) != "Weekly quota" || *q.Credits.Balance != "12.5" {
		t.Fatalf("incorrect quota mapping: %+v", q)
	}
	states := []appmodel.AccountDisplay{
		{Loading: true}, {Error: "error"}, {},
		{Identity: &appmodel.AccountIdentity{Type: "apiKey"}},
		{Identity: &appmodel.AccountIdentity{Type: "chatgpt", Email: "test@example.com", PlanType: "plus"}, Limits: data},
		{Identity: &appmodel.AccountIdentity{Type: "chatgpt"}, UsageError: "unavailable"},
		{Identity: &appmodel.AccountIdentity{Type: "chatgpt"}},
	}
	_ = states
}

func TestUnknownQuotaWindowIsNotInvented(t *testing.T) {
	if appmodel.QuotaLabel(&appmodel.QuotaWindow{}, 0) != "Primary quota" {
		t.Fatal("unknown duration presented as a known window")
	}
}
