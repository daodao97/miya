package integration

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/permissionflow"

	"miya/internal/controller"
	"miya/internal/desktop"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func TestRemoteControlSettingsChecksServicePermissions(t *testing.T) {
	app, tt := testApp(t)
	d := &testDispatch{}
	c := app.C.(*controller.Controller)
	c.Run = d.push
	conn, peer := net.Pipe()
	client := svcclient.NewClient(conn)
	t.Cleanup(func() { client.Close(); peer.Close() })
	c.Remote = client
	go func() {
		defer peer.Close()
		decode, encode := json.NewDecoder(peer), json.NewEncoder(peer)
		permission := permissionflow.Denied
		for {
			var req svcproto.Request
			if decode.Decode(&req) != nil {
				return
			}
			reply := map[string]any{"id": req.ID}
			if req.Op == desktop.PermissionStatusOp {
				reply["result"] = desktop.Permissions{
					Supported:       true,
					ScreenRecording: permission,
					Accessibility:   permissionflow.Granted,
				}
				if permission == permissionflow.Denied {
					permission = permissionflow.Granted
				} else {
					permission = permissionflow.Denied
				}
			}
			if encode.Encode(reply) != nil {
				return
			}
		}
	}()

	app.Section = "Settings"
	tt.Frame()
	if err := tt.Click("Remote Control"); err != nil {
		t.Fatal(err)
	}
	waitForStatus := func(want string, unwanted string) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			d.drain()
			tt.Frame()
			if tt.HasText(want) && (unwanted == "" || !tt.HasText(unwanted)) {
				return
			}
		}
		t.Fatalf("expected %q without %q, got %q", want, unwanted, tt.Texts())
	}
	waitForStatus("Not granted", "")
	for _, want := range []string{"Screen Recording", "Accessibility", "Granted"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	saveReconnectSnapshot(t, tt, "settings-remote-control")
	if err := tt.Click("Check again"); err != nil {
		t.Fatal(err)
	}
	waitForStatus("Granted", "Not granted")
	if err := tt.Click("Phone"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Remote Control"); err != nil {
		t.Fatal(err)
	}
	waitForStatus("Not granted", "")
	tt.Advance(3 * time.Second)
	waitForStatus("Granted", "Not granted")
	client.Close()
	waitForStatus("Background service disconnected", "Granted")
}
