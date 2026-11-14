package nsui

import (
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/uiruntime"
)

// idleEvery is how often the system's idle time is sampled. The controller
// asks it to decide whether a notification should show; a sample this old
// is close enough, and between samples the idle time grows with the clock.
const idleEvery = 15 * time.Second

// sampleIdle gives the controller the system's idle time, sampled on the
// engine goroutine. Where the platform has none, uiruntime.IdleTime stays
// nil and the user counts as present.
func sampleIdle(cx *core.Scope) {
	var idle, at atomic.Int64 // nanoseconds, and when they were sampled
	var sample func()
	sample = func() {
		cx.SystemIdleTime(func(d time.Duration, err error) {
			if err != nil {
				return // unsupported: stop sampling, leave IdleTime unset
			}
			idle.Store(int64(d))
			at.Store(time.Now().UnixNano())
			if uiruntime.IdleTime == nil {
				uiruntime.IdleTime = func() time.Duration {
					return time.Duration(idle.Load()) + time.Since(time.Unix(0, at.Load()))
				}
			}
			cx.AfterFunc(idleEvery, sample)
		})
	}
	// After the build that calls this, once the surface can take requests.
	cx.Post(sample)
}
