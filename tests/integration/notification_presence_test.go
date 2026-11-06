package integration

import (
	"context"
	"testing"
	"time"

	"miya/internal/svcproto"
)

func TestNotificationPresenceStopsWhenItsServiceConnectionCloses(t *testing.T) {
	c := testController()
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	defer c.Cancel()
	// The dispatcher belongs to this controller; a detached connection must
	// not keep consulting a window's global dispatch hook every ten seconds.
	c.Run = func(func()) {}
	cl := svcproto.NewDetachedClient()
	done := c.StartNotificationPresence(cl)
	cl.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old service connection retained its presence ticker")
	}
}
