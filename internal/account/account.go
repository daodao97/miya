// Package account is what Miya knows of the signed-in Codex account: who it
// is, its usage limits, and the reset credits it can spend.
package account

import (
	"time"
)

type QuotaWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int    `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

type QuotaSnapshot struct {
	LimitName string       `json:"limitName"`
	Primary   *QuotaWindow `json:"primary"`
	Secondary *QuotaWindow `json:"secondary"`
	Credits   *struct {
		HasCredits bool    `json:"hasCredits"`
		Unlimited  bool    `json:"unlimited"`
		Balance    *string `json:"balance"`
	} `json:"credits"`
}

type Limits struct {
	AccountID    string                   `json:"accountId"`
	RateLimits   *QuotaSnapshot           `json:"rateLimits"`
	ByID         map[string]QuotaSnapshot `json:"rateLimitsByLimitId"`
	ResetCredits *ResetCreditsSummary     `json:"rateLimitResetCredits"`
}

type Identity struct {
	Type     string `json:"type"`
	Email    string `json:"email"`
	PlanType string `json:"planType"`
}

type Display struct {
	Identity          *Identity
	Limits            Limits
	Loading           bool
	Refreshing        bool
	Error, UsageError string
	Notice            string
	Updated           time.Time
}

func SameAccount(a, b *Identity) bool {
	return a != nil && b != nil && a.Type == b.Type && a.Email == b.Email
}

type ResetCredit struct {
	ID          string `json:"id"`
	ResetType   string `json:"resetType"`
	Status      string `json:"status"`
	GrantedAt   int64  `json:"grantedAt"`
	ExpiresAt   *int64 `json:"expiresAt"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type ResetCreditsSummary struct {
	AvailableCount int           `json:"availableCount"`
	Credits        []ResetCredit `json:"credits"`
}

func (s *ResetCreditsSummary) Next() *ResetCredit {
	if s == nil || s.AvailableCount <= 0 {
		return nil
	}
	if i := NextResetCredit(s.Credits); i >= 0 {
		return &s.Credits[i]
	}
	return nil
}

// Match magpie's order: available Codex credits that expire first, then
// credits with no expiry. The count is authoritative even if details are capped.
func NextResetCredit(credits []ResetCredit) int {
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

type ResetAttempt struct {
	Account        Identity
	AccountID      string
	Credit         ResetCredit
	IdempotencyKey string
	Submitted      bool
}

func (a *ResetAttempt) Matches(data Display) bool {
	return a != nil && SameAccount(&a.Account, data.Identity) && (a.AccountID == "" || a.AccountID == data.Limits.AccountID)
}

type ResetAction struct {
	Attempt    *ResetAttempt
	Confirming bool
	Busy       bool
	Notice     string
	Failed     bool
}
