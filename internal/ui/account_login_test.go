package desktopui

import (
	"testing"
	"time"

	"miya/internal/agent/login"
	"miya/internal/appmodel"
	svcclient "miya/internal/service/client"
)

type loginTestHost struct {
	Host
	state   appmodel.WindowState
	targets map[*svcclient.Client]func(login.Request) (login.State, error)
}

func (h *loginTestHost) State() *appmodel.WindowState { return &h.state }
func (h *loginTestHost) RemoteHost() bool             { return true }
func (h *loginTestHost) AccountLoginTarget() func(login.Request) (login.State, error) {
	return h.targets[h.state.Remote]
}
func (h *loginTestHost) Dispatch(func()) {
	panic("abandoned response must be cancelled before dispatch")
}

func TestAccountLoginCancelsLateStartAtOriginalTargetAndClearsDraft(t *testing.T) {
	first, second := svcclient.NewDetachedClient(), svcclient.NewDetachedClient()
	defer first.Close()
	defer second.Close()
	requests := make(chan login.Request, 4)
	release := make(chan struct{})
	h := &loginTestHost{state: appmodel.WindowState{Remote: first}, targets: map[*svcclient.Client]func(login.Request) (login.State, error){}}
	h.targets[first] = func(r login.Request) (login.State, error) {
		requests <- r
		if r.Action == "start" {
			<-release
			return login.State{Agent: r.Agent, SessionID: "original", Phase: "waiting"}, nil
		}
		return login.State{Agent: r.Agent, SessionID: r.SessionID, Phase: "cancelled"}, nil
	}
	h.targets[second] = func(login.Request) (login.State, error) {
		t.Error("authorization reached newly selected server")
		return login.State{}, nil
	}
	app := &App{C: h}
	state := &AccountLoginUI{Draft: "private-draft"}
	s := &app.account
	s.init(app)
	s.loginClient, s.loginRemote, s.loginDone = first, true, make(chan struct{})
	s.logins = map[string]*AccountLoginUI{"claude": state}
	s.loginCall = h.AccountLoginTarget()
	s.loginAction("claude", "start")
	select {
	case r := <-requests:
		if r.Action != "start" {
			t.Fatal(r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("start not delivered")
	}
	h.state.Remote = second
	s.stopLogins()
	if state.Draft != "" {
		t.Fatal("switching servers retained the pasted authorization draft")
	}
	close(release)
	select {
	case r := <-requests:
		if r.Action != "cancel" || r.SessionID != "original" {
			t.Fatalf("late login was not cancelled at its original target: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("abandoned native login was left running")
	}
}
