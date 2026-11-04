package main

import (
	"context"
	"encoding/json"
	"time"

	"miya/internal/codex"
)

// The account page and the composer's quota ring show what the controller
// reads here, so that only the process owning the Codex connection talks to
// it; a window only shows the results and asks for a refresh.

func (c *chatController) parentContext() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

// refreshAccount reads the signed-in account and its usage; it never
// redeems a reset credit.
func (c *chatController) refreshAccount() {
	if c.remote != nil {
		c.forward("account", nil)
		return
	}
	c.accountVersion++
	request := c.accountVersion
	if c.accountCancel != nil {
		c.accountCancel()
	}
	client := c.client
	cached := c.accountCache
	if c.ready == nil || !c.ready() || client == nil {
		if !cached.Updated.IsZero() {
			cached.Notice = tr("Codex is not connected. Showing the last result.")
			c.accountState = cached
		} else {
			c.accountState = accountDisplay{Error: tr("Connect to Codex and try again.")}
		}
		invalidate()
		return
	}
	if cached.Updated.IsZero() {
		c.accountState = accountDisplay{Loading: true}
	} else {
		cached.Refreshing = true
		cached.Notice = ""
		c.accountState = cached
	}
	invalidate()
	ctx, stop := context.WithTimeout(c.parentContext(), 15*time.Second)
	c.accountCancel = stop
	go func() {
		defer stop()
		result, readOK := readAccountDisplay(ctx, client, cached)
		c.dispatch(func() {
			if c.accountVersion != request || c.client != client {
				return
			}
			if readOK {
				if result.Updated.Before(c.accountCache.Updated) {
					result = c.accountCache
				} else {
					c.accountCache = result
				}
			}
			c.accountState = result
			invalidate()
		})
	}()
}

// refreshQuota reads the account's rate limits for the composer's ring.
func (c *chatController) refreshQuota() {
	client := c.client
	if client == nil {
		return
	}
	c.quotaVersion++
	request := c.quotaVersion
	if c.quotaCancel != nil {
		c.quotaCancel()
	}
	if len(composerQuotaWindows(c.quotaLimits)) == 0 {
		c.quotaStatus = tr("Loading quota…")
	}
	ctx, stop := context.WithTimeout(c.parentContext(), 15*time.Second)
	c.quotaCancel = stop
	go func() {
		defer stop()
		var result accountLimits
		err := client.Call(ctx, "account/rateLimits/read", map[string]any{}, &result)
		c.dispatch(func() {
			if request != c.quotaVersion || client != c.client {
				return
			}
			c.quotaStatus = tr("No quota available")
			if err == nil {
				c.quotaLimits = result
			}
			invalidate()
		})
	}()
}

// quotaFrame applies Codex's account notifications to the ring.
func (c *chatController) quotaFrame(f codex.Frame) {
	if f.Method == "account/updated" {
		c.quotaLimits = accountLimits{}
		c.refreshQuota()
		return
	}
	var data accountLimits
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
	if meta.RateLimits.LimitID == "" || meta.RateLimits.LimitID == codexAccountLimitID {
		c.quotaVersion++
		c.quotaLimits = data
		invalidate()
	}
}
