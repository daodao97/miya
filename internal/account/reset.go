package account

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/locales"
)

type Caller interface {
	Call(context.Context, string, any, any) error
}

// Invoked only by the confirmation click. Revalidate the account and card
// before sending a specific credit ID. Retrying an uncertain result reuses
// the original key and ID even if that credit is no longer in the available list.
func ExecuteReset(ctx context.Context, client Caller, attempt ResetAttempt) (ResetExecution, error) {
	fresh, readOK := ReadDisplay(ctx, client, Display{})
	result := ResetExecution{Display: fresh, HaveDisplay: readOK, Submitted: attempt.Submitted}
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
	if ResetOutcomeText(response.Outcome) == "" {
		return result, errors.New(tr("Reset request returned an unknown result. Retry to confirm."))
	}
	result.Outcome = response.Outcome
	result.Display, _ = ReadDisplay(ctx, client, fresh)
	if result.Display.Notice != "" || result.Display.UsageError != "" {
		result.Display.Notice = tr("Reset confirmed. Latest usage and reset cards cannot be updated yet.")
	}
	return result, nil
}

func NewResetAttempt(data Display) (*ResetAttempt, error) {
	credit := data.Limits.ResetCredits.Next()
	if data.Identity == nil || data.Identity.Type != "chatgpt" || credit == nil {
		return nil, errors.New(tr("No reset card details available. Refresh and retry."))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, errors.New(tr("Unable to create a reset request. Try again."))
	}
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	key := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	return &ResetAttempt{Account: *data.Identity, AccountID: data.Limits.AccountID, Credit: *credit, IdempotencyKey: key}, nil
}

// Reads only. Opening Account and background refreshes never redeem credits.
func ReadDisplay(ctx context.Context, client Caller, cached Display) (Display, bool) {
	var identity ReadResult
	if err := client.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &identity); err != nil {
		if cached.Updated.IsZero() {
			return Display{Error: tr("Unable to read account information. Try again.")}, false
		}
		cached.Loading, cached.Refreshing = false, false
		cached.Notice = tr("Refresh failed. Showing the last result.")
		return cached, false
	}
	result := Display{Identity: identity.Account}
	if identity.Account != nil && identity.Account.Type == "chatgpt" {
		if err := client.Call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": false}, &result.Limits); err != nil {
			if SameAccount(identity.Account, cached.Identity) && !cached.Updated.IsZero() {
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

type ResetExecution struct {
	Display     Display
	HaveDisplay bool
	Submitted   bool
	Outcome     string
}

func ResetExpiryText(credit ResetCredit) string {
	if credit.ExpiresAt == nil {
		return tr("This reset card has no expiration.")
	}
	return tr("Next expiration: ") + time.Unix(*credit.ExpiresAt, 0).Local().Format("2006-01-02 15:04")
}

func ResetOutcomeText(outcome string) string {
	switch outcome {
	case "reset":
		return tr("Used 1 reset card. Quota windows reset.")
	case "nothingToReset":
		return tr("No quota windows need resetting. Card retained.")
	case "noCredit":
		return tr("No reset cards available for this account.")
	case "alreadyRedeemed":
		return tr("This reset request already completed. No additional card used.")
	default:
		return ""
	}
}

type ReadResult struct {
	Account *Identity `json:"account"`
}

func QuotaLabel(w *QuotaWindow, index int) string {
	if w.WindowDurationMins != nil {
		m := *w.WindowDurationMins
		if m == 300 {
			return tr("5-hour quota")
		}
		if m == 10080 {
			return tr("Weekly quota")
		}
		if m > 0 && m%1440 == 0 {
			return fmt.Sprintf(tr("%d-day quota"), m/1440)
		}
		if m > 0 && m%60 == 0 {
			return fmt.Sprintf(tr("%d-hour quota"), m/60)
		}
		if m > 0 {
			return fmt.Sprintf(tr("%d-minute quota"), m)
		}
	}
	if index == 0 {
		return tr("Primary quota")
	}
	return tr("Secondary quota")
}

func tr(s string) string { return locales.T(s) }
