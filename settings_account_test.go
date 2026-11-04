package main

import (
	"encoding/json"
	"testing"
)

func TestAccountQuotaResponse(t *testing.T) {
	var data accountLimits
	err := json.Unmarshal([]byte(`{"rateLimits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1800000000}},"rateLimitsByLimitId":{"codex":{"limitName":"Codex","primary":{"usedPercent":25,"windowDurationMins":300},"secondary":{"usedPercent":80,"windowDurationMins":10080},"credits":{"hasCredits":true,"unlimited":false,"balance":"12.5"}}}}`), &data)
	if err != nil {
		t.Fatal(err)
	}
	q := data.ByID["codex"]
	if q.Primary.UsedPercent != 25 || quotaLabel(q.Primary, 0) != "5-hour quota" || quotaLabel(q.Secondary, 1) != "Weekly quota" || *q.Credits.Balance != "12.5" {
		t.Fatalf("incorrect quota mapping: %+v", q)
	}
	states := []accountDisplay{
		{Loading: true}, {Error: "error"}, {},
		{Identity: &accountIdentity{Type: "apiKey"}},
		{Identity: &accountIdentity{Type: "chatgpt", Email: "test@example.com", PlanType: "plus"}, Limits: data},
		{Identity: &accountIdentity{Type: "chatgpt"}, UsageError: "unavailable"},
		{Identity: &accountIdentity{Type: "chatgpt"}},
	}
	_ = states
}
func TestUnknownQuotaWindowIsNotInvented(t *testing.T) {
	if quotaLabel(&quotaWindow{}, 0) != "Primary quota" {
		t.Fatal("unknown duration presented as a known window")
	}
}
