package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"miya/internal/account"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/controller"
)

const resetTestAccount = `{"account":{"type":"chatgpt","email":"test@example.com","planType":"plus"}}`

const resetTestLimits = `{"accountId":"account-1","rateLimits":{"primary":{"usedPercent":25,"windowDurationMins":300}},"rateLimitResetCredits":{"availableCount":4,"credits":[{"id":"later","resetType":"codexRateLimits","status":"available","grantedAt":1800000000,"expiresAt":1800100000},{"id":"soon","resetType":"codexRateLimits","status":"available","grantedAt":1800000000,"expiresAt":1800010000,"title":"Codex reset","description":"Reset your usage windows"}]}}`

const resetTestAfter = `{"accountId":"account-1","rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":300}},"rateLimitResetCredits":{"availableCount":3,"credits":[{"id":"later","resetType":"codexRateLimits","status":"available","grantedAt":1800000000,"expiresAt":1800100000}]}}`

type accountCallStep struct {
	method   string
	response string
	err      error
	check    func(any)
}

type scriptedAccountCaller struct {
	t     *testing.T
	steps []accountCallStep
}

func (s *scriptedAccountCaller) Call(_ context.Context, method string, params any, out any) error {
	s.t.Helper()
	if len(s.steps) == 0 {
		s.t.Fatalf("unexpected request: %s", method)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if method != step.method {
		s.t.Fatalf("requested %s, want %s", method, step.method)
	}
	if step.check != nil {
		step.check(params)
	}
	if step.err != nil {
		return step.err
	}
	return json.Unmarshal([]byte(step.response), out)
}

func resetTestDisplay(t *testing.T) account.Display {
	t.Helper()
	var data account.Limits
	if err := json.Unmarshal([]byte(resetTestLimits), &data); err != nil {
		t.Fatal(err)
	}
	return account.Display{Identity: &account.Identity{Type: "chatgpt", Email: "test@example.com"}, Limits: data, Updated: time.Now()}
}

func TestAccountReadUsesRealResetDetailsWithoutRedeeming(t *testing.T) {
	rpc := &scriptedAccountCaller{t: t, steps: []accountCallStep{
		{method: "account/read", response: resetTestAccount},
		{method: "account/rateLimits/read", response: resetTestLimits, check: func(params any) {
			if params.(map[string]any)["excludeResetCreditDetails"] != false {
				t.Fatal("Account did not request reset-credit details")
			}
		}},
	}}
	data, ok := account.ReadDisplay(context.Background(), rpc, account.Display{})
	if !ok || data.Limits.ResetCredits.AvailableCount != 4 || data.Limits.ResetCredits.Next().ID != "soon" || len(rpc.steps) != 0 {
		t.Fatalf("reset-credit response was not mapped: %+v", data)
	}
	// The available count can exceed the capped detail list, and null details
	// must remain unknown rather than invented cards or a zero count.
	var countOnly account.Limits
	if err := json.Unmarshal([]byte(`{"rateLimitResetCredits":{"availableCount":2,"credits":null}}`), &countOnly); err != nil {
		t.Fatal(err)
	}
	if countOnly.ResetCredits.AvailableCount != 2 || countOnly.ResetCredits.Next() != nil {
		t.Fatal("count-only credits were treated as complete details")
	}
}

func TestAccountRefreshFailureKeepsPreviousDisplay(t *testing.T) {
	cached := resetTestDisplay(t)
	for _, steps := range [][]accountCallStep{
		{{method: "account/read", err: errors.New("offline")}},
		{{method: "account/read", response: resetTestAccount}, {method: "account/rateLimits/read", err: errors.New("offline")}},
	} {
		rpc := &scriptedAccountCaller{t: t, steps: steps}
		result, _ := account.ReadDisplay(context.Background(), rpc, cached)
		if result.Loading || result.Refreshing || result.Identity == nil || result.Limits.ResetCredits != cached.Limits.ResetCredits || result.Limits.RateLimits != cached.Limits.RateLimits || !result.Updated.Equal(cached.Updated) {
			t.Fatal("background refresh removed the previous account or usage")
		}
	}
}

func TestNextResetCreditIgnoresUnavailableAndUsesEarliestExpiry(t *testing.T) {
	soon, later := int64(1800010000), int64(1800100000)
	credits := []account.ResetCredit{
		{ID: "used", Status: "redeemed", ResetType: "codexRateLimits", ExpiresAt: &soon},
		{ID: "forever", Status: "available", ResetType: "codexRateLimits"},
		{ID: "later", Status: "available", ResetType: "codexRateLimits", ExpiresAt: &later},
		{ID: "soon", Status: "available", ResetType: "codexRateLimits", ExpiresAt: &soon},
		{ID: "unknown", Status: "available", ResetType: "unknown", ExpiresAt: &soon},
	}
	if i := account.NextResetCredit(credits); i != 3 {
		t.Fatalf("selected %d, want earliest available Codex credit", i)
	}
	if i := account.NextResetCredit(credits[:2]); i != 1 {
		t.Fatalf("selected %d, want non-expiring available credit", i)
	}
	if i := account.NextResetCredit(credits[:1]); i != -1 || account.NextResetCredit(nil) != -1 {
		t.Fatal("an unavailable credit was selected")
	}
}

func TestResetRequiresConfirmationAndKeepsRetryIdentity(t *testing.T) {
	c := &controller.Controller{WindowState: appmodel.WindowState{Ready: func() bool { return true }}, AccountCache: resetTestDisplay(t), Client: &codex.Client{}}
	c.ConfirmAccountReset() // No user confirmation: no request can be sent.
	c.AskAccountReset()     // Opening the confirmation also sends no request.
	if !c.AccountReset.Confirming || c.AccountReset.Attempt == nil || c.AccountReset.Attempt.Credit.ID != "soon" || len(c.AccountReset.Attempt.IdempotencyKey) != 36 {
		t.Fatal("confirmation did not bind the chosen credit")
	}
	c.CancelAccountReset()
	if c.AccountReset.Attempt != nil {
		t.Fatal("cancel before submission kept an unused attempt")
	}
	c.AskAccountReset()
	attempt := c.AccountReset.Attempt
	attempt.Submitted = true
	c.CancelAccountReset()
	c.AskAccountReset()
	if c.AccountReset.Attempt.IdempotencyKey != attempt.IdempotencyKey || !c.AccountReset.Confirming {
		t.Fatal("an uncertain request was replaced with a new attempt")
	}
	c.AccountReset.Busy = true
	c.ConfirmAccountReset() // Busy guard must block duplicate submission.
}

func TestResetRedeemsSelectedCreditAndReloadsServerData(t *testing.T) {
	attempt, err := account.NewResetAttempt(resetTestDisplay(t))
	if err != nil {
		t.Fatal(err)
	}
	rpc := &scriptedAccountCaller{t: t, steps: []accountCallStep{
		{method: "account/read", response: resetTestAccount},
		{method: "account/rateLimits/read", response: resetTestLimits},
		{method: "account/rateLimitResetCredit/consume", response: `{"outcome":"reset"}`, check: func(params any) {
			p := params.(map[string]any)
			if p["creditId"] != "soon" || p["idempotencyKey"] != attempt.IdempotencyKey {
				t.Fatalf("wrong credit or retry key: %+v", p)
			}
		}},
		{method: "account/read", response: resetTestAccount},
		{method: "account/rateLimits/read", response: resetTestAfter},
	}}
	result, err := account.ExecuteReset(context.Background(), rpc, *attempt)
	if err != nil || result.Outcome != "reset" || !result.Submitted || result.Display.Limits.ResetCredits.AvailableCount != 3 || result.Display.Limits.RateLimits.Primary.UsedPercent != 0 || len(rpc.steps) != 0 {
		t.Fatalf("redemption did not use the refreshed server result: %+v %v", result, err)
	}
}

func TestResetBlocksChangedAccountOrSelectedCredit(t *testing.T) {
	attempt, err := account.NewResetAttempt(resetTestDisplay(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, limits := range []string{
		strings.ReplaceAll(resetTestLimits, "account-1", "account-2"),
		resetTestAfter,
	} {
		rpc := &scriptedAccountCaller{t: t, steps: []accountCallStep{
			{method: "account/read", response: resetTestAccount},
			{method: "account/rateLimits/read", response: limits},
		}}
		result, err := account.ExecuteReset(context.Background(), rpc, *attempt)
		if err == nil || result.Submitted {
			t.Fatal("redemption was allowed after the account or card changed")
		}
	}
}

func TestResetRetryReusesKeyAfterLostResponse(t *testing.T) {
	attempt, err := account.NewResetAttempt(resetTestDisplay(t))
	if err != nil {
		t.Fatal(err)
	}
	check := func(params any) {
		p := params.(map[string]any)
		if p["creditId"] != attempt.Credit.ID || p["idempotencyKey"] != attempt.IdempotencyKey {
			t.Fatal("retry changed its credit or idempotency key")
		}
	}
	first := &scriptedAccountCaller{t: t, steps: []accountCallStep{
		{method: "account/read", response: resetTestAccount},
		{method: "account/rateLimits/read", response: resetTestLimits},
		{method: "account/rateLimitResetCredit/consume", err: context.DeadlineExceeded, check: check},
	}}
	result, err := account.ExecuteReset(context.Background(), first, *attempt)
	if err == nil || !result.Submitted {
		t.Fatal("lost response was not retained as an uncertain submission")
	}
	attempt.Submitted = result.Submitted
	retry := &scriptedAccountCaller{t: t, steps: []accountCallStep{
		{method: "account/read", response: resetTestAccount},
		{method: "account/rateLimits/read", response: resetTestAfter},
		{method: "account/rateLimitResetCredit/consume", response: `{"outcome":"alreadyRedeemed"}`, check: check},
		{method: "account/read", response: resetTestAccount},
		{method: "account/rateLimits/read", response: resetTestAfter},
	}}
	result, err = account.ExecuteReset(context.Background(), retry, *attempt)
	if err != nil || result.Outcome != "alreadyRedeemed" {
		t.Fatalf("idempotent retry failed: %+v %v", result, err)
	}
}

func TestReadRealAccountResetCredits(t *testing.T) {
	if os.Getenv("CODEX_ACCOUNT_READ_INTEGRATION") != "1" {
		t.Skip("set CODEX_ACCOUNT_READ_INTEGRATION=1 for a read-only account check")
	}
	binary, err := codex.Binary()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := codex.Start(ctx, binary, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	data, ok := account.ReadDisplay(ctx, client, account.Display{})
	if !ok || data.Error != "" || data.UsageError != "" {
		t.Fatalf("read-only account check failed: %s %s", data.Error, data.UsageError)
	}
	if data.Identity == nil || data.Identity.Type != "chatgpt" {
		t.Skip("the configured Codex account is not signed in with ChatGPT")
	}
	if summary := data.Limits.ResetCredits; summary != nil {
		t.Logf("real reset-credit summary: available=%d, details=%d, selectable=%t", summary.AvailableCount, len(summary.Credits), summary.Next() != nil)
	} else {
		t.Log("the server did not provide a reset-credit summary for this account")
	}
}
