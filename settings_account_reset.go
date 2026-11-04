package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"miya/internal/codex"
)

type resetCredit struct {
	ID          string `json:"id"`
	ResetType   string `json:"resetType"`
	Status      string `json:"status"`
	GrantedAt   int64  `json:"grantedAt"`
	ExpiresAt   *int64 `json:"expiresAt"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type resetCreditsSummary struct {
	AvailableCount int           `json:"availableCount"`
	Credits        []resetCredit `json:"credits"`
}

// Match magpie's order: available Codex credits that expire first, then
// credits with no expiry. The count is authoritative even if details are capped.
func nextResetCredit(credits []resetCredit) int {
	first := -1
	for i, credit := range credits {
		if credit.Status != "available" || credit.ResetType != "codexRateLimits" || credit.ID == "" {
			continue
		}
		if first < 0 || credit.ExpiresAt != nil && (credits[first].ExpiresAt == nil || *credit.ExpiresAt < *credits[first].ExpiresAt) {
			first = i
		}
	}
	return first
}

func (s *resetCreditsSummary) next() *resetCredit {
	if s == nil || s.AvailableCount <= 0 {
		return nil
	}
	if i := nextResetCredit(s.Credits); i >= 0 {
		return &s.Credits[i]
	}
	return nil
}

type accountCaller interface {
	Call(context.Context, string, any, any) error
}

// Reads only. Opening Account and background refreshes never redeem credits.
func readAccountDisplay(ctx context.Context, client accountCaller, cached accountDisplay) (accountDisplay, bool) {
	var identity accountRead
	if err := client.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &identity); err != nil {
		if cached.Updated.IsZero() {
			return accountDisplay{Error: tr("Unable to read account information. Try again.")}, false
		}
		cached.Loading, cached.Refreshing = false, false
		cached.Notice = tr("Refresh failed. Showing the last result.")
		return cached, false
	}
	result := accountDisplay{Identity: identity.Account}
	if identity.Account != nil && identity.Account.Type == "chatgpt" {
		if err := client.Call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": false}, &result.Limits); err != nil {
			if sameAccount(identity.Account, cached.Identity) && !cached.Updated.IsZero() {
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

type accountResetAttempt struct {
	Account        accountIdentity
	AccountID      string
	Credit         resetCredit
	IdempotencyKey string
	Submitted      bool
}

func newAccountResetAttempt(data accountDisplay) (*accountResetAttempt, error) {
	credit := data.Limits.ResetCredits.next()
	if data.Identity == nil || data.Identity.Type != "chatgpt" || credit == nil {
		return nil, errors.New(tr("No reset card details available. Refresh and retry."))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, errors.New(tr("Unable to create a reset request. Try again."))
	}
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	key := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	return &accountResetAttempt{Account: *data.Identity, AccountID: data.Limits.AccountID, Credit: *credit, IdempotencyKey: key}, nil
}

func (a *accountResetAttempt) matches(data accountDisplay) bool {
	return a != nil && sameAccount(&a.Account, data.Identity) && (a.AccountID == "" || a.AccountID == data.Limits.AccountID)
}

type accountResetAction struct {
	Attempt    *accountResetAttempt
	Confirming bool
	Busy       bool
	Notice     string
	Failed     bool
}

type resetExecution struct {
	Display     accountDisplay
	HaveDisplay bool
	Submitted   bool
	Outcome     string
}

// Invoked only by the confirmation click. Revalidate the account and card
// before sending a specific credit ID. Retrying an uncertain result reuses
// the original key and ID even if that credit is no longer in the available list.
func executeAccountReset(ctx context.Context, client accountCaller, attempt accountResetAttempt) (resetExecution, error) {
	fresh, readOK := readAccountDisplay(ctx, client, accountDisplay{})
	result := resetExecution{Display: fresh, HaveDisplay: readOK, Submitted: attempt.Submitted}
	if !readOK || fresh.UsageError != "" {
		return result, errors.New(tr("Unable to verify the latest account and reset cards. Refresh and retry."))
	}
	if !attempt.matches(fresh) {
		return result, errors.New(tr("The signed-in account changed. Select a reset card again."))
	}
	if !attempt.Submitted {
		credit := fresh.Limits.ResetCredits.next()
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
	if resetOutcomeText(response.Outcome) == "" {
		return result, errors.New(tr("Reset request returned an unknown result. Retry to confirm."))
	}
	result.Outcome = response.Outcome
	result.Display, _ = readAccountDisplay(ctx, client, fresh)
	if result.Display.Notice != "" || result.Display.UsageError != "" {
		result.Display.Notice = tr("Reset confirmed. Latest usage and reset cards cannot be updated yet.")
	}
	return result, nil
}

func resetOutcomeText(outcome string) string {
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

func (c *chatController) setAccountReset(action accountResetAction) {
	c.accountReset = action
	invalidate()
}

func (c *chatController) askAccountReset() {
	if c.remote != nil {
		c.forward("accountAsk", nil)
		return
	}
	if c.accountReset.Busy || !c.ready() || c.client == nil {
		return
	}
	action := c.accountReset
	if action.Attempt == nil || !action.Attempt.Submitted || !action.Attempt.matches(c.accountCache) {
		attempt, err := newAccountResetAttempt(c.accountCache)
		if err != nil {
			c.setAccountReset(accountResetAction{Notice: err.Error(), Failed: true})
			return
		}
		action = accountResetAction{Attempt: attempt}
	}
	action.Confirming = true
	c.setAccountReset(action)
}

func (c *chatController) cancelAccountReset() {
	if c.remote != nil {
		c.forward("accountCancel", nil)
		return
	}
	if c.accountReset.Busy {
		return
	}
	action := c.accountReset
	action.Confirming = false
	if action.Attempt != nil && !action.Attempt.Submitted {
		action.Attempt = nil
	}
	c.setAccountReset(action)
}

func (c *chatController) confirmAccountReset() {
	if c.remote != nil {
		c.forward("accountConfirm", nil)
		return
	}
	action := c.accountReset
	if action.Busy || !action.Confirming || action.Attempt == nil {
		return
	}
	if !c.ready() || c.client == nil || c.ctx == nil {
		action.Notice, action.Failed = tr("Codex is not connected. Connect and retry."), true
		c.setAccountReset(action)
		return
	}
	attempt, client := *action.Attempt, c.client
	action.Busy, action.Notice, action.Failed = true, "", false
	c.setAccountReset(action)
	// The user's request outlives the Account view, so switching tabs cannot
	// lose its result or create a fresh idempotency key for an in-flight request.
	ctx, stop := context.WithTimeout(c.ctx, 30*time.Second)
	go func() {
		defer stop()
		result, err := executeAccountReset(ctx, client, attempt)
		c.dispatch(func() {
			if result.HaveDisplay && c.client == client && !result.Display.Updated.Before(c.accountCache.Updated) {
				c.accountCache = result.Display
			}
			attempt.Submitted = result.Submitted
			finished := accountResetAction{Attempt: &attempt, Confirming: true}
			if err != nil {
				finished.Notice, finished.Failed = err.Error(), true
			} else {
				finished = accountResetAction{Notice: resetOutcomeText(result.Outcome)}
			}
			if attempt.Account.Email != "" {
				finished.Notice = attempt.Account.Email + ": " + finished.Notice
			}
			c.setAccountReset(finished)
			c.accountState = c.accountCache
			if result.Outcome != "" && c.client == client && result.Display.Notice == "" && result.Display.UsageError == "" {
				params, _ := json.Marshal(result.Display.Limits)
				c.quotaFrame(codex.Frame{Method: "account/rateLimits/updated", Params: params})
			}
			if result.Outcome != "" && (result.Display.Notice != "" || result.Display.UsageError != "") {
				c.refreshAccount()
			}
		})
	}()
}

func resetExpiryText(credit resetCredit) string {
	if credit.ExpiresAt == nil {
		return tr("This reset card has no expiration.")
	}
	return tr("Next expiration: ") + time.Unix(*credit.ExpiresAt, 0).Local().Format("2006-01-02 15:04")
}
