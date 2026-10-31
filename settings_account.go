package main

import (
	"fmt"
	"time"
)

type accountIdentity struct {
	Type     string `json:"type"`
	Email    string `json:"email"`
	PlanType string `json:"planType"`
}
type accountRead struct {
	Account *accountIdentity `json:"account"`
}
type quotaWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int    `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}
type quotaSnapshot struct {
	LimitName string       `json:"limitName"`
	Primary   *quotaWindow `json:"primary"`
	Secondary *quotaWindow `json:"secondary"`
	Credits   *struct {
		HasCredits bool    `json:"hasCredits"`
		Unlimited  bool    `json:"unlimited"`
		Balance    *string `json:"balance"`
	} `json:"credits"`
}
type accountLimits struct {
	AccountID    string                   `json:"accountId"`
	RateLimits   *quotaSnapshot           `json:"rateLimits"`
	ByID         map[string]quotaSnapshot `json:"rateLimitsByLimitId"`
	ResetCredits *resetCreditsSummary     `json:"rateLimitResetCredits"`
}
type accountDisplay struct {
	Identity          *accountIdentity
	Limits            accountLimits
	Loading           bool
	Refreshing        bool
	Error, UsageError string
	Notice            string
	Updated           time.Time
}

func sameAccount(a, b *accountIdentity) bool {
	return a != nil && b != nil && a.Type == b.Type && a.Email == b.Email
}
func quotaLabel(w *quotaWindow, index int) string {
	if w.WindowDurationMins != nil {
		m := *w.WindowDurationMins
		if m == 300 {
			return "5 小时额度"
		}
		if m == 10080 {
			return "每周额度"
		}
		if m > 0 && m%1440 == 0 {
			return fmt.Sprintf("%d 天额度", m/1440)
		}
		if m > 0 && m%60 == 0 {
			return fmt.Sprintf("%d 小时额度", m/60)
		}
		if m > 0 {
			return fmt.Sprintf("%d 分钟额度", m)
		}
	}
	if index == 0 {
		return "主要额度"
	}
	return "次要额度"
}
