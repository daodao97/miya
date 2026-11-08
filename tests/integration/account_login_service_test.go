//go:build !windows

package integration

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"miya/internal/agent/login"
	"miya/internal/appmodel"
	"miya/internal/controller"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func TestNativeLoginServiceOwnsDeviceCodeAndKeepsRestartBusy(t *testing.T) {
	f := newServiceFixture(t)
	binary, err := filepath.Abs("testdata/account-login-cli.py")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("CODEX_BINARY", binary)
	t.Setenv("CODEX_HOME", home)
	cl, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var started login.State
	if err := cl.Call("accountLogin", login.Request{Action: "start", Agent: "codex"}, &started); err != nil {
		t.Fatal(err)
	}
	var state login.State
	deadline := time.After(10 * time.Second)
	for {
		if err := cl.Call("accountLogin", login.Request{Action: "status", Agent: "codex", SessionID: started.SessionID}, &state); err != nil {
			t.Fatal(err)
		}
		if state.Phase == "waiting" {
			break
		}
		if !state.Active() {
			t.Fatalf("device login failed: %+v", state)
		}
		select {
		case <-deadline:
			t.Fatal("native device login did not start")
		default:
			runtime.Gosched()
		}
	}
	if state.UserCode != "ABCD-1234" {
		t.Fatal("device code did not cross service transport")
	}
	busy := false
	f.serv.Do(func() { busy = f.C.ServiceBusy() })
	if !busy {
		t.Fatal("idle restart could interrupt native authorization")
	}
	var snapshot svcproto.State
	if err := cl.Call("state", nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(snapshot)
	if string(data) == "" || containsSecret(data, "ABCD-1234") || containsSecret(data, "fixture-login") {
		t.Fatal("one-time login state leaked into broadcast snapshots")
	}
	if err := cl.Call("accountLogin", login.Request{Action: "cancel", Agent: "codex", SessionID: started.SessionID}, &state); err != nil {
		t.Fatal(err)
	}
	if state.Active() || state.URL != "" || state.UserCode != "" {
		t.Fatal("cancel retained active challenge")
	}
	if err := os.WriteFile(filepath.Join(home, "fixture-signed-in"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call("accountLogin", login.Request{Action: "check", Agent: "codex"}, &state); err != nil {
		t.Fatal(err)
	}
	if state.Identity == nil || state.Identity.Email != filepath.Base(home)+"@example.invalid" {
		t.Fatalf("status did not use the service's native account directory: %+v", state)
	}
}

func containsSecret(data []byte, value string) bool { return strings.Contains(string(data), value) }

func loginEndpoint(t *testing.T, tag string) (*svcclient.Client, <-chan login.Request) {
	t.Helper()
	a, b := net.Pipe()
	cl := svcclient.NewClient(a)
	t.Cleanup(func() { cl.Close(); b.Close() })
	requests := make(chan login.Request, 4)
	go func() {
		dec, enc := json.NewDecoder(bufio.NewReader(b)), json.NewEncoder(b)
		for {
			var r svcproto.Request
			if dec.Decode(&r) != nil {
				return
			}
			var req login.Request
			_ = json.Unmarshal(r.Args, &req)
			requests <- req
			data, _ := json.Marshal(login.State{Agent: req.Agent, SessionID: tag, Phase: "waiting"})
			if enc.Encode(svcproto.Envelope{ID: r.ID, Result: data}) != nil {
				return
			}
		}
	}()
	return cl, requests
}

func TestLoginTargetIsCapturedBeforeAWindowSwitchesServers(t *testing.T) {
	a, requestsA := loginEndpoint(t, "server-a")
	b, requestsB := loginEndpoint(t, "server-b")
	c := &controller.Controller{WindowState: appmodel.WindowState{Remote: a}}
	targetA := c.AccountLoginTarget()
	c.Remote = b
	state, err := targetA(login.Request{Agent: "claude", Action: "submit", SessionID: "original", Code: "one-time#state"})
	if err != nil || state.SessionID != "server-a" {
		t.Fatal("authorization followed current server instead of its original target", state, err)
	}
	req := <-requestsA
	if req.Code != "one-time#state" || req.SessionID != "original" {
		t.Fatal("native code was not scoped to its original attempt")
	}
	select {
	case <-requestsB:
		t.Fatal("code reached a different server")
	default:
	}
}
