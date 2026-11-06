// Package quota defines subscription readings and provider contracts.
package quota

import (
	"math"
	"time"

	"miya/internal/agent/account"
)

type Window struct {
	Name  string
	Used  float64
	Reset string
	// Structured times are retained for pacing; display strings may omit
	// the year or use a locale and must not be parsed back into timestamps.
	DurationMins int64
	ResetsAt     int64
}

type Reading struct {
	Agent   string
	Plan    string
	Windows []Window
	At      time.Time
	Error   string
	Resets  *account.ResetCreditsSummary
	// Identity and config location are held in memory to bind a confirmed reset
	// to the account that was displayed, even if the CLI signs in elsewhere.
	AccountKey string
	Account    string
	Home       string
}

func Percent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Max(0, math.Min(100, v))
}
