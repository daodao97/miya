package quota

import (
	"math"
	"time"
)

func TimeProgress(w Window, now time.Time) (float64, bool) {
	if w.DurationMins <= 0 || w.ResetsAt <= 0 {
		return 0, false
	}
	remainingSeconds := float64(w.ResetsAt) - float64(now.Unix())
	elapsed := 100 * (1 - remainingSeconds/(float64(w.DurationMins)*60))
	return math.Max(0, math.Min(100, elapsed)), true
}
