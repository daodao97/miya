package view

import (
	"time"

	"github.com/egoist/mygo/ui"
)

type messageScroll struct {
	position ui.ScrollState
	lastY    float32
	ready    bool
	until    time.Time
}

// The message indicator follows movement instead of hover on both desktop
// and phone. Keep it briefly after scrolling stops.
func messageScrollbars(c *ui.Context, list ui.Element) {
	s := ui.Local(list, "message-scrollbars", func() messageScroll { return messageScroll{} })
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
