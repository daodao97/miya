package appmodel

import (
	account "miya/internal/account"
)

// Prefer a five-day bucket when supplied; otherwise show the short window first.
func ComposerQuotaWindows(l account.Limits) []*account.QuotaWindow {
	q := l.RateLimits
	if v, ok := l.ByID[CodexAccountLimitID]; ok {
		q = &v
	}
	if q == nil {
		return nil
	}
	var out []*account.QuotaWindow
	for _, duration := range []int{7200, 300, 10080} {
		for _, w := range []*account.QuotaWindow{q.Primary, q.Secondary} {
			if w != nil && w.WindowDurationMins != nil && *w.WindowDurationMins == duration {
				out = append(out, w)
			}
		}
	}
	return out
}
