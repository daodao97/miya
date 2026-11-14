package integration

import (
	"encoding/json"
	"net"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/controller"
	"miya/internal/selfupdate"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func updateTestClient(t *testing.T, hello svcproto.Hello) (*svcclient.Client, func() []string) {
	t.Helper()
	conn, peer := net.Pipe()
	cl := svcclient.NewClient(conn)
	t.Cleanup(func() { cl.Close(); peer.Close() })
	var mu sync.Mutex
	var actions []string
	go func() {
		defer peer.Close()
		dec, enc := json.NewDecoder(peer), json.NewEncoder(peer)
		status := selfupdate.Status{Supported: true, Current: hello.Release, Latest: "1.2.3", Phase: "idle"}
		for {
			var req svcproto.Request
			if dec.Decode(&req) != nil {
				return
			}
			var result any
			switch req.Op {
			case "hello":
				result = hello
			case "miyaUpdate":
				var args struct{ Action, Version string }
				if json.Unmarshal(req.Args, &args) != nil {
					return
				}
				mu.Lock()
				actions = append(actions, args.Action+":"+args.Version)
				mu.Unlock()
				if args.Action == "install" {
					status.Phase = "waiting"
				}
				if args.Action == "check" {
					status.Phase = "available"
				}
				result = status
			default:
				t.Errorf("unexpected update operation %s", req.Op)
				return
			}
			if enc.Encode(map[string]any{"id": req.ID, "result": result}) != nil {
				return
			}
		}
	}()
	return cl, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), actions...) }
}

func TestRemoteMiyaUpdateRequiresClickAndVerifiesReleaseAfterReconnect(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	c := app.C.(*controller.Controller)
	c.Run = d.push
	c.Mirror = &svcclient.Mirror{RemoteHost: true}
	old, actions := updateTestClient(t, svcproto.Hello{PID: 100, Release: "1.0.0", SelfUpdate: true, Busy: true})
	c.Remote = old
	c.RememberDraft("draft survives remote update")
	tt := ui.NewTester(app.MiyaUpdateSettings, 760, 600)
	waitRestartSettings(t, d, tt, func() bool { return app.MiyaUpdate.Status.Phase == "available" && !app.MiyaUpdate.Calling })
	if got := actions(); len(got) != 2 || got[0] != "status:" || got[1] != "check:1.2.3" {
		t.Fatalf("connection installed without a click: %v", got)
	}
	if err := tt.Click("Upgrade target"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.MiyaUpdate.Status.Phase == "waiting" })
	if got := actions(); len(got) < 3 || got[2] != "install:1.2.3" {
		t.Fatalf("selected release was not pinned: %v", got)
	}
	if app.MiyaUpdate.Status.Phase == "done" {
		t.Fatal("installation alone claimed success")
	}
	wrong, _ := updateTestClient(t, svcproto.Hello{PID: 200, Release: "1.0.0", SelfUpdate: true})
	c.Remote = wrong
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.MiyaUpdate.Err != "" })
	if app.MiyaUpdate.Status.Phase == "done" {
		t.Fatal("new PID alone claimed update success")
	}
	next, _ := updateTestClient(t, svcproto.Hello{PID: 300, Release: "1.2.3", SelfUpdate: true})
	c.Remote = next
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.MiyaUpdate.Status.Phase == "done" })
	if !tt.HasText("The target is running the requested Miya version.") || c.Draft() != "draft survives remote update" {
		t.Fatal("verified version or preserved draft missing")
	}
}

func TestRemoteMiyaUpdateOldHostAndLocalWorkspace(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	c := app.C.(*controller.Controller)
	c.Run = d.push
	cl, actions := updateTestClient(t, svcproto.Hello{PID: 100})
	c.Remote = cl
	tt := ui.NewTester(app.MiyaUpdateSettings, 760, 600)
	if tt.HasText("Upgrade target") || len(actions()) != 0 {
		t.Fatal("local workspace offered a remote update")
	}
	c.Mirror = &svcclient.Mirror{RemoteHost: true}
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.MiyaUpdate.Status.Phase == "unsupported" })
	if !tt.HasText("Install an updated Miya on the target computer once to enable remote upgrades.") || len(actions()) != 0 {
		t.Fatal("legacy host was sent an unsupported update operation")
	}
}
