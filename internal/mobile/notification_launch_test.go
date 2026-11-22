package mobile

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

// The tap that launched the app arrives before the saved desktops are read;
// connecting to them reset the desktop state, which dropped what it opens.
func TestNotificationTappedAtLaunchOpensOnceLoaded(t *testing.T) {
	m := NewMobileApp()
	m.Router().Transition = ui.TransitionNone
	m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { m.Disconnect(); m.FlushCache(true) })
	identity := tailcat.NewPrivateKey()
	identity.Public.RegionID = 1
	link := remote.Link(identity.Public.Addr())
	var saved MobileDesktops
	item, err := saved.Remember(link, "Mac")
	if err != nil {
		t.Fatal(err)
	}
	saved.Active = item.ID

	m.OpenNotification(map[string]string{"thread": "4", "serverId": "launch", "desktop": item.ID})
	m.finishLoad(saved, "", t.TempDir(), nil)
	if m.Link != link {
		t.Fatal("loading did not connect to the saved desktop")
	}
	m.ApplyPhoneState(phoneproto.State{Projects: []model.ProjectSnapshot{{Path: "/p", Threads: []model.Thread{{ID: 4, ServerID: "launch", Title: "Launch"}}}}})
	if m.Viewer.Current().ServerID != "launch" || m.Router().Path() != "/thread" {
		t.Fatalf("notification tapped at launch did not open its conversation: path %q, open %q", m.Router().Path(), m.Viewer.Current().ServerID)
	}
}
