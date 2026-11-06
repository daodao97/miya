package account

import (
	"context"
	"errors"
	"time"

	"miya/internal/agent/account"
	codex "miya/internal/agent/codex/native"
	"miya/locales"
)

// Invoked only by the confirmation click. Revalidate the account and card
// before sending a specific credit ID. Retrying an uncertain result reuses
// the original key and ID even if that credit is no longer in the available list.
func ExecuteReset(ctx context.Context, client account.Caller, attempt account.ResetAttempt) (account.ResetExecution, error) {
	fresh, readOK := ReadDisplay(ctx, client, account.Display{})
	result := account.ResetExecution{Display: fresh, HaveDisplay: readOK, Submitted: attempt.Submitted}
	if !readOK || fresh.UsageError != "" {
		return result, errors.New(tr("Unable to verify the latest account and reset cards. Refresh and retry."))
	}
	if !attempt.Matches(fresh) {
		return result, errors.New(tr("The signed-in account changed. Select a reset card again."))
	}
	if !attempt.Submitted {
		credit := fresh.Limits.ResetCredits.Next()
		if credit == nil || credit.ID != attempt.Credit.ID {
			return result, errors.New(tr("The selected reset card changed. Cancel and select it again."))
		}
	}
	var response struct {
		Outcome string `json:"outcome"`
	}
	result.Submitted = true
	err := client.Call(ctx, "account/rateLimitResetCredit/consume", map[string]any{
		"creditId": attempt.Credit.ID, "idempotencyKey": attempt.IdempotencyKey,
	}, &response)
	if err != nil {
		var rpc *codex.RPCError
		if errors.As(err, &rpc) && rpc.Code == -32601 {
			result.Submitted = false
			return result, errors.New(tr("This Codex version does not support reset cards. Update the Codex CLI."))
		}
		return result, errors.New(tr("Reset request is unconfirmed. Retry to retrieve the result."))
	}
	if account.ResetOutcomeText(response.Outcome) == "" {
		return result, errors.New(tr("Reset request returned an unknown result. Retry to confirm."))
	}
	result.Outcome = response.Outcome
	result.Display, _ = ReadDisplay(ctx, client, fresh)
	if result.Display.Notice != "" || result.Display.UsageError != "" {
		result.Display.Notice = tr("Reset confirmed. Latest usage and reset cards cannot be updated yet.")
	}
	return result, nil
}

// Reads only. Opening Account and background refreshes never redeem credits.
func ReadDisplay(ctx context.Context, client account.Caller, cached account.Display) (account.Display, bool) {
	var identity account.ReadResult
	if err := client.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &identity); err != nil {
		if cached.Updated.IsZero() {
			return account.Display{Error: tr("Unable to read account information. Try again.")}, false
		}
		cached.Loading, cached.Refreshing = false, false
		cached.Notice = tr("Refresh failed. Showing the last result.")
		return cached, false
	}
	result := account.Display{Identity: identity.Account}
	if identity.Account != nil && identity.Account.Type == "chatgpt" {
		if err := client.Call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": false}, &result.Limits); err != nil {
			if account.SameAccount(identity.Account, cached.Identity) && !cached.Updated.IsZero() {
				result.Limits, result.Updated = cached.Limits, cached.Updated
				result.UsageError = tr("Unable to update usage and reset cards. Showing the last result.")
			} else {
				result.UsageError = tr("Usage and reset cards are unavailable. Refresh later.")
			}
		}
	}
	if result.Updated.IsZero() {
		result.Updated = time.Now()
	}
	return result, true
}

func tr(s string) string { return locales.T(s) }
