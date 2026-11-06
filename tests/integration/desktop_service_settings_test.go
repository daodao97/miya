//go:build !windows

package integration

import (
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/controller"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	desktopui "miya/internal/ui"
)

// Only the test pipe is closed: these tests never restart the real service.
func restartTestClient(t *testing.T, hello svcproto.Hello, helloError string) (*svcclient.Client, func() []string) {
	t.Helper()
	conn, peer := net.Pipe()
	cl := svcclient.NewClient(conn)
	t.Cleanup(func() { cl.Close(); peer.Close() })
	var mu sync.Mutex
	var methods []string
	go func() {
		defer peer.Close()
		decoder, encoder := json.NewDecoder(peer), json.NewEncoder(peer)
		for {
			var req svcproto.Request
			if decoder.Decode(&req) != nil {
				return
			}
			mu.Lock()
			methods = append(methods, req.Op)
			mu.Unlock()
			reply := map[string]any{"id": req.ID}
			switch req.Op {
			case "hello":
				if helloError != "" {
					reply["error"] = helloError
				} else {
					reply["result"] = hello
				}
			case "quit":
				return // shutdown can close the connection before replying
			case "quitWhenIdle":
				if !hello.Busy {
					return
				}
			}
			if encoder.Encode(reply) != nil {
				return
			}
		}
	}()
	return cl, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

func waitRestartSettings(t *testing.T, d *testDispatch, tt *ui.Tester, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		d.drain()
		tt.Frame()
		if done() {
			return
		}
	}
	t.Fatal("service restart UI did not reach the expected state")
}

func TestServiceSettingsIdleRestartAndReconnect(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	app.C.(*controller.Controller).Run = d.push
	old, methods := restartTestClient(t, svcproto.Hello{PID: 100}, "")
	app.C.(*controller.Controller).Remote = old
	app.C.(*controller.Controller).RememberDraft("unsent draft")
	app.C.(*controller.Controller).SetAttachments([]string{"/tmp/unsent.txt"})
	tt := ui.NewTester(app.ServiceSettings, 760, 400)
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Requested && !app.ServiceRestart.Checking })
	if got := methods(); !reflect.DeepEqual(got, []string{"hello", "quitWhenIdle"}) || app.ServiceRestart.Confirming {
		t.Fatalf("idle restart did not use the safe shutdown path: %v", got)
	}
	if app.ServiceRestart.Completed {
		t.Fatal("shutdown alone was reported as a successful restart")
	}
	next, _ := restartTestClient(t, svcproto.Hello{PID: 200}, "")
	app.C.(*controller.Controller).Remote = next
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Completed })
	if !tt.HasText("Background service restarted and reconnected.") || app.ServiceRestart.Err != "" {
		t.Fatal("new service process was not verified")
	}
	if app.C.(*controller.Controller).Draft() != "unsent draft" || !reflect.DeepEqual(app.C.(*controller.Controller).Attachments(), []string{"/tmp/unsent.txt"}) {
		t.Fatal("restart changed the unsent draft or attachments")
	}
}

func TestServiceSettingsBusyRestartRequiresChoice(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	app.C.(*controller.Controller).Run = d.push
	cl, methods := restartTestClient(t, svcproto.Hello{PID: 100, Busy: true}, "")
	app.C.(*controller.Controller).Remote = cl
	tt := ui.NewTester(app.ServiceSettings, 760, 500)
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Confirming })
	if !reflect.DeepEqual(methods(), []string{"hello"}) || !tt.HasText("Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.") {
		t.Fatal("busy service was stopped before the user chose how to restart")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if app.ServiceRestart.Confirming || !reflect.DeepEqual(methods(), []string{"hello"}) {
		t.Fatal("canceling a restart sent a shutdown request")
	}
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Confirming })
	if err := tt.Click("Restart when idle"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Requested && !app.ServiceRestart.Checking })
	if !desktopui.ServiceConnected(cl) || !reflect.DeepEqual(methods(), []string{"hello", "hello", "quitWhenIdle"}) {
		t.Fatal("waiting for idle interrupted the busy service")
	}
	if err := tt.Click("Restart now…"); err != nil {
		t.Fatal(err)
	}
	if !app.ServiceRestart.Confirming || len(methods()) != 3 {
		t.Fatal("overriding a scheduled restart skipped confirmation")
	}
	if err := tt.Click("Restart now"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return !app.ServiceRestart.Checking && !desktopui.ServiceConnected(cl) })
	if got := methods(); !reflect.DeepEqual(got, []string{"hello", "hello", "quitWhenIdle", "quit"}) {
		t.Fatalf("explicit restart did not request normal service shutdown: %v", got)
	}
}

func TestServiceSettingsFailuresAndStandaloneMode(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	app.C.(*controller.Controller).Run = d.push
	tt := ui.NewTester(app.ServiceSettings, 760, 400)
	if !tt.HasText("Standalone mode has no separate background service.") {
		t.Fatal("standalone mode did not explain why restart is unavailable")
	}
	_ = tt.Click("Restart")
	if app.ServiceRestart.Checking {
		t.Fatal("standalone mode tried to restart a service")
	}
	cl, _ := restartTestClient(t, svcproto.Hello{}, "test hello failure")
	app.C.(*controller.Controller).Remote = cl
	tt.Frame()
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Err != "" })
	if !tt.HasText("Restart failed: test hello failure") || app.ServiceRestart.Checking || app.ServiceRestart.Requested {
		t.Fatal("restart error was hidden or left the controls disabled")
	}
	old, _ := restartTestClient(t, svcproto.Hello{PID: 100}, "")
	next, _ := restartTestClient(t, svcproto.Hello{PID: 100}, "")
	app.ServiceRestart = desktopui.ServiceRestartUI{Client: old, Pid: 100, Requested: true}
	app.C.(*controller.Controller).Remote = next
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.ServiceRestart.Err != "" })
	if app.ServiceRestart.Completed || !strings.Contains(app.ServiceRestart.Err, "has not exited") {
		t.Fatal("reconnecting to the same process was reported as a restart")
	}
}
