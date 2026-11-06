package main

import (
	"context"
	"encoding/json"
	"time"

	"miya/internal/codex"
)

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
	if action.Attempt == nil || !action.Attempt.Submitted || !action.Attempt.Matches(c.accountCache) {
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
