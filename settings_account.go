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
