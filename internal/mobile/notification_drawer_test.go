package mobile

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/tailscale/tailcat"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func drawerNotificationApp(t *testing.T) *App {
	t.Helper()
	m := NewMobileApp()
	m.Loaded, m.DataDir = true, t.TempDir()
	m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { m.Disconnect(); m.FlushCache(true) })
	identity := tailcat.NewPrivateKey()
	identity.Public.RegionID = 1
	m.Connect(remote.Link(identity.Public.Addr()))
	m.Connected = true
	m.ApplyPhoneState(phoneproto.State{Projects: []model.ProjectSnapshot{
		{Name: "a", Path: "/a", Current: true, Threads: []model.Thread{{ID: 1, Title: "one", ServerID: "s1"}, {ID: 2, Title: "two", ServerID: "s2"}}},
	}})
	return m
}

// The sidebar was open as the app went away; a notification tapped to come
// back opens its conversation.
func TestNotificationTappedWithDrawerOpenOpensItsConversation(t *testing.T) {
	for _, start := range []string{"/", "/thread"} {
		t.Run(start, func(t *testing.T) {
			m := drawerNotificationApp(t)
			tt := ui.NewTester(m.ViewRoot, 390, 844)
			tt.Frame()
			if start == "/thread" {
				m.OpenThread(m.State.Projects[0].Threads[0])
				tt.Frame()
				tt.Advance(time.Second)
			}
			m.Menu = true
			tt.Frame()
			tt.Advance(time.Second)
			if !m.DrawerVisible {
				t.Fatal("setup: the sidebar did not open")
			}
			m.enterBackground(false)
			m.OpenNotification(map[string]string{"thread": "2", "serverId": "s2", "desktop": m.CurrentDesktopID()})
			m.enterForeground()
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			if m.Router().Path() != "/thread" || m.Viewer.Current().ServerID != "s2" || m.Menu || m.DrawerVisible {
				t.Fatalf("path %q, open %q, menu %v, sidebar visible %v, offset %v, dragging %v", m.Router().Path(), m.Viewer.Current().ServerID, m.Menu, m.DrawerVisible, m.DrawerOffset, m.DrawerDragging)
			}
		})
	}
}
