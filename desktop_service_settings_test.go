//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Only the test pipe is closed: these tests never restart the real service.
func restartTestClient(t *testing.T, hello svcHello, helloError string) (*serviceClient, func() []string) {
	t.Helper()
	conn, peer := net.Pipe()
	cl := newServiceClient(conn)
	t.Cleanup(func() { cl.Close(); peer.Close() })
	var mu sync.Mutex
	var methods []string
	go func() {
		defer peer.Close()
		decoder, encoder := json.NewDecoder(peer), json.NewEncoder(peer)
		for {
			var req svcRequest
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
	app.c.run = d.push
	old, methods := restartTestClient(t, svcHello{PID: 100}, "")
	app.c.remote = old
	app.c.rememberDraft("unsent draft")
	app.c.setAttachments([]string{"/tmp/unsent.txt"})
	tt := ui.NewTester(app.serviceSettings, 760, 400)
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.requested && !app.serviceRestart.checking })
	if got := methods(); !reflect.DeepEqual(got, []string{"hello", "quitWhenIdle"}) || app.serviceRestart.confirming {
		t.Fatalf("idle restart did not use the safe shutdown path: %v", got)
	}
	if app.serviceRestart.completed {
		t.Fatal("shutdown alone was reported as a successful restart")
	}
	next, _ := restartTestClient(t, svcHello{PID: 200}, "")
	app.c.remote = next
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.completed })
	if !tt.HasText("Background service restarted and reconnected.") || app.serviceRestart.err != "" {
		t.Fatal("new service process was not verified")
	}
	if app.c.draft() != "unsent draft" || !reflect.DeepEqual(app.c.attachments(), []string{"/tmp/unsent.txt"}) {
		t.Fatal("restart changed the unsent draft or attachments")
	}
}

func TestServiceSettingsBusyRestartRequiresChoice(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	app.c.run = d.push
	cl, methods := restartTestClient(t, svcHello{PID: 100, Busy: true}, "")
	app.c.remote = cl
	tt := ui.NewTester(app.serviceSettings, 760, 500)
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.confirming })
	if !reflect.DeepEqual(methods(), []string{"hello"}) || !tt.HasText("Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.") {
		t.Fatal("busy service was stopped before the user chose how to restart")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if app.serviceRestart.confirming || !reflect.DeepEqual(methods(), []string{"hello"}) {
		t.Fatal("canceling a restart sent a shutdown request")
	}
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.confirming })
	if err := tt.Click("Restart when idle"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.requested && !app.serviceRestart.checking })
	if !serviceConnected(cl) || !reflect.DeepEqual(methods(), []string{"hello", "hello", "quitWhenIdle"}) {
		t.Fatal("waiting for idle interrupted the busy service")
	}
	if err := tt.Click("Restart now…"); err != nil {
		t.Fatal(err)
	}
	if !app.serviceRestart.confirming || len(methods()) != 3 {
		t.Fatal("overriding a scheduled restart skipped confirmation")
	}
	if err := tt.Click("Restart now"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return !app.serviceRestart.checking && !serviceConnected(cl) })
	if got := methods(); !reflect.DeepEqual(got, []string{"hello", "hello", "quitWhenIdle", "quit"}) {
		t.Fatalf("explicit restart did not request normal service shutdown: %v", got)
	}
}

func TestServiceSettingsFailuresAndStandaloneMode(t *testing.T) {
	app, _ := testApp(t)
	d := &testDispatch{}
	app.c.run = d.push
	tt := ui.NewTester(app.serviceSettings, 760, 400)
	if !tt.HasText("Standalone mode has no separate background service.") {
		t.Fatal("standalone mode did not explain why restart is unavailable")
	}
	_ = tt.Click("Restart")
	if app.serviceRestart.checking {
		t.Fatal("standalone mode tried to restart a service")
	}
	cl, _ := restartTestClient(t, svcHello{}, "test hello failure")
	app.c.remote = cl
	tt.Frame()
	if err := tt.Click("Restart"); err != nil {
		t.Fatal(err)
	}
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.err != "" })
	if !tt.HasText("Restart failed: test hello failure") || app.serviceRestart.checking || app.serviceRestart.requested {
		t.Fatal("restart error was hidden or left the controls disabled")
	}
	old, _ := restartTestClient(t, svcHello{PID: 100}, "")
	next, _ := restartTestClient(t, svcHello{PID: 100}, "")
	app.serviceRestart = serviceRestartUI{client: old, pid: 100, requested: true}
	app.c.remote = next
	tt.Frame()
	waitRestartSettings(t, d, tt, func() bool { return app.serviceRestart.err != "" })
	if app.serviceRestart.completed || !strings.Contains(app.serviceRestart.err, "has not exited") {
		t.Fatal("reconnecting to the same process was reported as a restart")
	}
}
