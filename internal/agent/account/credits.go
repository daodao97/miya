package account

import (
	"slices"
	"time"
)

// Normalize removes unusable detail rows while preserving the authoritative count.
func (r *ResetCreditsSummary) Normalize(now time.Time) {
	r.AvailableCount = max(0, r.AvailableCount)
	if r.Credits == nil {
		return
	}
	kept := make([]ResetCredit, 0, len(r.Credits))
	seen := map[string]bool{}
	for _, card := range r.Credits {
		if card.ID == "" || seen[card.ID] || card.Status != "available" || card.ResetType != "codexRateLimits" || card.ExpiresAt != nil && *card.ExpiresAt <= now.Unix() {
			continue
		}
		seen[card.ID] = true
		kept = append(kept, card)
	}
	slices.SortStableFunc(kept, func(a, b ResetCredit) int {
		if a.ExpiresAt == nil {
			if b.ExpiresAt == nil {
				return 0
			}
			return 1
		}
		if b.ExpiresAt == nil {
			return -1
		}
		return time.Unix(*a.ExpiresAt, 0).Compare(time.Unix(*b.ExpiresAt, 0))
	})
	r.Credits = kept
}
