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
			return accountDisplay{Error: "无法读取账号信息，请重试。"}, false
		}
		cached.Loading, cached.Refreshing = false, false
		cached.Notice = "刷新失败，显示上次读取的结果。"
		return cached, false
	}
	result := accountDisplay{Identity: identity.Account}
	if identity.Account != nil && identity.Account.Type == "chatgpt" {
		if err := client.Call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": false}, &result.Limits); err != nil {
			if sameAccount(identity.Account, cached.Identity) && !cached.Updated.IsZero() {
				result.Limits, result.Updated = cached.Limits, cached.Updated
				result.UsageError = "暂时无法更新用量和重置卡，显示上次结果。"
			} else {
				result.UsageError = "暂时无法获取用量和重置卡，请稍后刷新。"
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
		return nil, errors.New("暂无可使用的重置卡详情，请刷新后重试。")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, errors.New("暂时无法创建使用请求，请重试。")
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
		return result, errors.New("无法核实最新账号和重置卡，请刷新后重试。")
	}
	if !attempt.matches(fresh) {
		return result, errors.New("当前登录账号已变化，请重新选择重置卡。")
	}
	if !attempt.Submitted {
		credit := fresh.Limits.ResetCredits.next()
		if credit == nil || credit.ID != attempt.Credit.ID {
			return result, errors.New("所选重置卡已变化，请取消后重新选择。")
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
			return result, errors.New("当前 Codex 版本不支持重置卡兑换，请更新 Codex CLI。")
		}
		return result, errors.New("使用请求尚未获得确认，请重试以获取结果。")
	}
	if resetOutcomeText(response.Outcome) == "" {
		return result, errors.New("使用请求返回了未知结果，请重试以获取确认。")
	}
	result.Outcome = response.Outcome
	result.Display, _ = readAccountDisplay(ctx, client, fresh)
	if result.Display.Notice != "" || result.Display.UsageError != "" {
		result.Display.Notice = "使用结果已确认，最新用量和重置卡暂时无法更新。"
	}
	return result, nil
}

func resetOutcomeText(outcome string) string {
	switch outcome {
	case "reset":
		return "已使用 1 张重置卡，额度窗口已重置。"
	case "nothingToReset":
		return "当前没有需要重置的额度窗口，重置卡已保留。"
	case "noCredit":
		return "账号已没有可用的重置卡。"
	case "alreadyRedeemed":
		return "这次使用请求已经完成，未重复兑换。"
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
		action.Notice, action.Failed = "Codex 尚未连接，请连接后重试。", true
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
				finished.Notice = attempt.Account.Email + "：" + finished.Notice
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
		return "此重置卡无到期时间。"
	}
	return "最近到期：" + time.Unix(*credit.ExpiresAt, 0).Local().Format("2006-01-02 15:04")
}
