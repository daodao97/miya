//go:build darwin && !ios

package nsui

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

var idleClock = sync.OnceValue(func() func(uint32, uint32) float64 {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		return nil
	}
	sym, err := purego.Dlsym(lib, "CGEventSourceSecondsSinceLastEventType")
	if err != nil {
		return nil
	}
	var clock func(uint32, uint32) float64
	purego.RegisterFunc(&clock, sym)
	return clock
})

func systemIdleTime() (time.Duration, bool) {
	clock := idleClock()
	if clock == nil {
		return 0, false
	}
	// Combined session state, any input event.
	seconds := clock(0, ^uint32(0))
	return time.Duration(seconds * float64(time.Second)), seconds >= 0
}
