package controller

import (
	"context"
	"encoding/json"
	"time"

	"miya/internal/agent/account"
	codex "miya/internal/agent/codex/native"
	"miya/internal/uiruntime"
)

func (c *Controller) setAccountReset(action account.ResetAction) {
	c.AccountReset = action
	uiruntime.Invalidate()
}

func (c *Controller) AskAccountReset() {
	if c.Remote != nil {
		c.forward("accountAsk", nil)
		return
	}
	if c.AccountReset.Busy || !c.Ready() || c.Client == nil {
		return
	}
	action := c.AccountReset
	if action.Attempt == nil || !action.Attempt.Submitted || !action.Attempt.Matches(c.AccountCache) {
		attempt, err := account.NewResetAttempt(c.AccountCache)
		if err != nil {
			c.setAccountReset(account.ResetAction{Notice: err.Error(), Failed: true})
			return
		}
		action = account.ResetAction{Attempt: attempt}
	}
	action.Confirming = true
	c.setAccountReset(action)
}

func (c *Controller) CancelAccountReset() {
	if c.Remote != nil {
		c.forward("accountCancel", nil)
		return
	}
	if c.AccountReset.Busy {
		return
	}
	action := c.AccountReset
	action.Confirming = false
	if action.Attempt != nil && !action.Attempt.Submitted {
		action.Attempt = nil
	}
	c.setAccountReset(action)
}

func (c *Controller) ConfirmAccountReset() {
	if c.Remote != nil {
		c.forward("accountConfirm", nil)
		return
	}
	action := c.AccountReset
	if action.Busy || !action.Confirming || action.Attempt == nil {
		return
	}
	if !c.Ready() || c.Client == nil || c.Ctx == nil {
		action.Notice, action.Failed = tr("Codex is not connected. Connect and retry."), true
		c.setAccountReset(action)
		return
	}
	attempt, client := *action.Attempt, c.Client
	source := c.accountSource()
	action.Busy, action.Notice, action.Failed = true, "", false
	c.setAccountReset(action)
	// The user's request outlives the Account view, so switching tabs cannot
	// lose its result or create a fresh idempotency key for an in-flight request.
	ctx, stop := context.WithTimeout(c.Ctx, 30*time.Second)
	go func() {
		defer stop()
		result, err := source.Reset(ctx, attempt)
		c.Dispatch(func() {
			if result.HaveDisplay && c.Client == client && !result.Display.Updated.Before(c.AccountCache.Updated) {
				c.AccountCache = result.Display
			}
			attempt.Submitted = result.Submitted
			finished := account.ResetAction{Attempt: &attempt, Confirming: true}
			if err != nil {
				finished.Notice, finished.Failed = err.Error(), true
			} else {
				finished = account.ResetAction{Notice: account.ResetOutcomeText(result.Outcome)}
			}
			if attempt.Account.Email != "" {
				finished.Notice = attempt.Account.Email + ": " + finished.Notice
			}
			c.setAccountReset(finished)
			c.AccountState = c.AccountCache
			if result.Outcome != "" && c.Client == client && result.Display.Notice == "" && result.Display.UsageError == "" {
				params, _ := json.Marshal(result.Display.Limits)
				c.quotaFrame(codex.Frame{Method: "account/rateLimits/updated", Params: params})
			}
			if result.Outcome != "" && (result.Display.Notice != "" || result.Display.UsageError != "") {
				c.RefreshAccount()
			}
		})
	}()
}
