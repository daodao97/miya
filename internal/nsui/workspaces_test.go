package nsui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/service/workspaces"
	"miya/internal/uiruntime"
)

func twoWorkspaces() workspaces.Store {
	return workspaces.Store{Active: workspaces.Local, Items: []workspaces.Workspace{
		{ID: workspaces.Local, Name: "Local"},
		{ID: "prod", Name: "prod-us", Link: "miya://connect?token=x"},
	}}
}

// startHub shows the shell with a workspace hub whose connections connect
// answers.
func startHub(t *testing.T, connect func(context.Context, workspaces.Workspace) (*svcclient.Client, svcproto.State, error)) (*nsgotest.App, *workspaceHub) {
	t.Helper()
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"})
	h.state.Ctx = context.Background()
	t.Cleanup(func() {
		uiruntime.SetAppWindow(nil)
		current = nil
	})
	var hub *workspaceHub
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		hub = newWorkspaceHub(cx, h, twoWorkspaces(), nil)
		hub.connect = connect
		return core.Dynamic(func(cx *core.Scope) core.Node {
			return root(cx, hub.active.Get(), workspaceSelector(cx, hub))
		})
	}, nsgotest.Size(900, 600))
	return app, hub
}

// A remote workspace that cannot be reached says why and keeps the window
// on the workspace it showed.
func TestWorkspaceConnectionFailureShows(t *testing.T) {
	app, hub := startHub(t, func(context.Context, workspaces.Workspace) (*svcclient.Client, svcproto.State, error) {
		return nil, svcproto.State{}, errors.New("dial prod-us: refused")
	})
	root := hub.active.GetUntracked()
	app.Press("id:workspace-switcher")
	app.Settle()
	app.Press("id:workspace-prod")
	app.Settle()
	if !app.Exists("id:workspace-error") || !strings.Contains(app.Find("id:workspace-error").Text, "refused") {
		t.Fatalf("no connection error; texts %q", app.Texts())
	}
	if hub.active.GetUntracked() != root || hub.busy.GetUntracked() {
		t.Fatal("failed connection switched workspace or stayed busy")
	}
}

// Adding a workspace needs a connection code, and a code that is not
// Miya's says so in the dialog.
func TestWorkspaceAddValidatesCode(t *testing.T) {
	app, hub := startHub(t, func(context.Context, workspaces.Workspace) (*svcclient.Client, svcproto.State, error) {
		t.Error("connected with an invalid code")
		return nil, svcproto.State{}, errors.New("unexpected")
	})
	app.Press("id:workspace-switcher")
	app.Settle()
	app.Press("id:add-workspace")
	app.Settle()
	if app.Enabled("id:workspace-connect") {
		t.Fatal("connect enabled without a code")
	}
	app.Type("label:Workspace name", "staging")
	app.Type("label:Connection code", "miya://connect?token=y")
	app.Settle()
	if !app.Enabled("id:workspace-connect") {
		t.Fatal("connect disabled with a code")
	}
	app.Press("id:workspace-connect")
	app.Settle()
	if hub.busy.GetUntracked() || !app.Exists("id:workspace-dialog-error") {
		t.Fatalf("invalid code not reported in the dialog; texts %q", app.Texts())
	}
}

// While a workspace connects the connection can be cancelled, and its late
// answer is ignored.
func TestWorkspaceConnectionCancels(t *testing.T) {
	release := make(chan struct{})
	app, hub := startHub(t, func(context.Context, workspaces.Workspace) (*svcclient.Client, svcproto.State, error) {
		<-release
		return nil, svcproto.State{}, errors.New("too late")
	})
	app.Press("id:workspace-switcher")
	app.Settle()
	app.Press("id:workspace-prod")
	app.Settle()
	if !app.Exists("id:workspace-connecting") {
		t.Fatalf("no progress while connecting; texts %q", app.Texts())
	}
	app.Press("label:Cancel connection")
	app.Settle()
	close(release)
	app.Settle()
	if hub.busy.GetUntracked() || app.Exists("id:workspace-connecting") || app.Exists("id:workspace-error") {
		t.Fatal("cancelled connection still shows or reported its late failure")
	}
}
