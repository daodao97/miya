package controller

import (
	context "context"
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	uiruntime "miya/internal/uiruntime"
	time "time"
)

func (c *Controller) setAccountReset(action appmodel.AccountResetAction) {
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
		attempt, err := appmodel.NewAccountResetAttempt(c.AccountCache)
		if err != nil {
			c.setAccountReset(appmodel.AccountResetAction{Notice: err.Error(), Failed: true})
			return
		}
		action = appmodel.AccountResetAction{Attempt: attempt}
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
	action.Busy, action.Notice, action.Failed = true, "", false
	c.setAccountReset(action)
	// The user's request outlives the Account view, so switching tabs cannot
	// lose its result or create a fresh idempotency key for an in-flight request.
	ctx, stop := context.WithTimeout(c.Ctx, 30*time.Second)
	go func() {
		defer stop()
		result, err := appmodel.ExecuteAccountReset(ctx, client, attempt)
		c.Dispatch(func() {
			if result.HaveDisplay && c.Client == client && !result.Display.Updated.Before(c.AccountCache.Updated) {
				c.AccountCache = result.Display
			}
			attempt.Submitted = result.Submitted
			finished := appmodel.AccountResetAction{Attempt: &attempt, Confirming: true}
			if err != nil {
				finished.Notice, finished.Failed = err.Error(), true
			} else {
				finished = appmodel.AccountResetAction{Notice: appmodel.ResetOutcomeText(result.Outcome)}
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
