package nsmobile

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

func (m *App) sheetBack(id, title, to string) core.Node {
	return m.button(id, title, nil, func() { m.pages.Open(to) })
}

// A sheet owns its polling timer. Closing it stops reads without discarding
// cached data or an uncertain mutation's identity.
func (m *App) pollSheet(cx *core.Scope, poll func()) {
	active := true
	var stop func() bool
	var tick func()
	tick = func() {
		if !active {
			return
		}
		m.TickFeatures()
		poll()
		m.change()
		stop = cx.AfterFunc(time.Second, tick)
	}
	cx.Post(tick)
	cx.Cleanup(func() {
		active = false
		if stop != nil {
			stop()
		}
	})
}
