package widgets

import (
	"time"

	"github.com/egoist/mygo/ui"
)

type scrollActivity struct {
	position ui.ScrollState
	lastY    float32
	ready    bool
	until    time.Time
}

// ScrollbarsWhileScrolling shows the list's scrollbar only while it moves,
// instead of on hover, and keeps it briefly after scrolling stops.
func ScrollbarsWhileScrolling(c *ui.Context, list ui.Element) {
	s := ui.Local(list, "scrollbars-while-scrolling", func() scrollActivity { return scrollActivity{} })
	list.TrackScroll(&s.position)
	now := c.Now()
	if s.ready && s.position.Y != s.lastY {
		s.until = now.Add(600 * time.Millisecond)
	}
	s.lastY, s.ready = s.position.Y, s.position.MaxY > 0
	visible := now.Before(s.until)
	list.Scrollbars(visible).KeepScrollbars(visible)
	if visible {
		c.After(s.until.Sub(now))
	}
}
