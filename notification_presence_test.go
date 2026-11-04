package main

import (
	"context"
	"testing"
	"time"
)

func TestNotificationPresenceStopsWhenItsServiceConnectionCloses(t *testing.T) {
	c := testController()
	c.ctx, c.cancel = context.WithCancel(context.Background())
	defer c.cancel()
	// The dispatcher belongs to this controller; a detached connection must
	// not keep consulting a window's global dispatch hook every ten seconds.
	c.run = func(func()) {}
	cl := &serviceClient{closed: make(chan struct{})}
	done := c.startNotificationPresence(cl)
	close(cl.closed)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old service connection retained its presence ticker")
	}
}
