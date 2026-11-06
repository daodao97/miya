package controller

import (
	"context"
	"encoding/json"
	"time"

	"miya/internal/agent/account"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/uiruntime"
)

func (c *Controller) parentContext() context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// refreshAccount reads the signed-in account and its usage; it never
// redeems a reset credit.
func (c *Controller) RefreshAccount() {
	if c.Remote != nil {
		c.forward("account", nil)
		return
	}
	c.accountVersion++
	request := c.accountVersion
	if c.accountCancel != nil {
		c.accountCancel()
	}
	client := c.Client
	cached := c.AccountCache
	source := c.accountSource()
	if c.Ready == nil || !c.Ready() || client == nil || source == nil {
		if !cached.Updated.IsZero() {
			cached.Notice = tr("Codex is not connected. Showing the last result.")
			c.AccountState = cached
		} else {
			c.AccountState = account.Display{Error: tr("Connect to Codex and try again.")}
		}
		uiruntime.Invalidate()
		return
	}
	if cached.Updated.IsZero() {
		c.AccountState = account.Display{Loading: true}
	} else {
		cached.Refreshing = true
		cached.Notice = ""
		c.AccountState = cached
	}
	uiruntime.Invalidate()
	ctx, stop := context.WithTimeout(c.parentContext(), 15*time.Second)
	c.accountCancel = stop
	go func() {
		defer stop()
		result, readOK := source.Read(ctx, cached)
		c.Dispatch(func() {
			if c.accountVersion != request || c.Client != client {
				return
			}
			if readOK {
				if result.Updated.Before(c.AccountCache.Updated) {
					result = c.AccountCache
				} else {
					c.AccountCache = result
				}
			}
			c.AccountState = result
			uiruntime.Invalidate()
		})
	}()
}

// refreshQuota reads the account's rate limits for the composer's ring.
func (c *Controller) refreshQuota() {
	client := c.Client
	source := c.accountSource()
	if client == nil || source == nil {
		return
	}
	c.quotaVersion++
	request := c.quotaVersion
	if c.quotaCancel != nil {
		c.quotaCancel()
	}
	if len(appmodel.ComposerQuotaWindows(c.QuotaLimits)) == 0 {
		c.quotaStatus = tr("Loading quota…")
	}
	ctx, stop := context.WithTimeout(c.parentContext(), 15*time.Second)
	c.quotaCancel = stop
	go func() {
		defer stop()
		result, err := source.ReadLimits(ctx)
		c.Dispatch(func() {
			if request != c.quotaVersion || client != c.Client {
				return
			}
			c.quotaStatus = tr("No quota available")
			if err == nil {
				c.QuotaLimits = result
			}
			uiruntime.Invalidate()
		})
	}()
}

// quotaFrame applies Codex's account notifications to the ring.
func (c *Controller) quotaFrame(f codex.Frame) {
	if f.Method == "account/updated" {
		c.QuotaLimits = account.Limits{}
		c.refreshQuota()
		return
	}
	var data account.Limits
	if json.Unmarshal(f.Params, &data) != nil || data.RateLimits == nil {
		return
	}
	// Model-specific buckets must not replace the account-wide quota.
	var meta struct {
		RateLimits struct {
			LimitID string `json:"limitId"`
		} `json:"rateLimits"`
	}
	_ = json.Unmarshal(f.Params, &meta)
	if meta.RateLimits.LimitID == "" || meta.RateLimits.LimitID == appmodel.CodexAccountLimitID {
		c.quotaVersion++
		c.QuotaLimits = data
		uiruntime.Invalidate()
	}
}
