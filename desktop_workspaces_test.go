//go:build !windows

package main

import (
	context "context"
	json "encoding/json"
	errors "errors"
	appdir "miya/internal/appdir"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	remote "miya/internal/remote"
	desktopui "miya/internal/ui"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func TestDesktopWorkspacesMigrateAndKeepLocalSelection(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	link := testDesktopLink()
	legacy, _ := json.Marshal(map[string]string{"link": link})
	if err := os.WriteFile(filepath.Join(appdir.Dir(), "remote-client.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := appmodel.LoadDesktopWorkspaces()
	if err != nil || len(store.Items) != 2 || store.Current().Link != link {
		t.Fatal("legacy workspace did not migrate", err)
	}
	addr, _ := remote.ParseLink(link)
	item, err := store.Remember(string(addr), "prod-us")
	if err != nil || len(store.Items) != 2 || item.Name != "prod-us" {
		t.Fatal("same pairing created multiple workspaces", err)
	}
	store.Active = appmodel.LocalDesktopWorkspace
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := appmodel.LoadDesktopWorkspaces()
	if err != nil || loaded.Active != appmodel.LocalDesktopWorkspace || len(loaded.Items) != 2 || loaded.Items[1].Name != "prod-us" {
		t.Fatal("local selection or saved name lost", err)
	}
	if appmodel.RemoteServerLink() != "" {
		t.Fatal("legacy target resurrected after selecting local")
	}
	info, err := os.Stat(appmodel.RemoteServerPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("workspace capabilities are not private", err)
	}
}

func TestDesktopWorkspacesRejectCorruptRegistry(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	data := []byte(`{"version":99,"items":[]}`)
	if err := os.WriteFile(appmodel.RemoteServerPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := appmodel.LoadDesktopWorkspaces(); err == nil {
		t.Fatal("unknown registry version silently accepted")
	}
	if err := appmodel.SaveRemoteServer(testDesktopLink()); err == nil {
		t.Fatal("corrupt registry overwritten by adding a workspace")
	}
	after, _ := os.ReadFile(appmodel.RemoteServerPath())
	if string(after) != string(data) {
		t.Fatal("failed migration changed saved capabilities")
	}
}

func desktopWorkspaceFixture(t *testing.T) (*desktopui.App, *desktopui.DesktopWorkspaces, *testDispatch) {
	t.Helper()
	t.Setenv("MIYA_DIR", t.TempDir())
	app, _ := testApp(t)
	dispatch := &testDispatch{}
	app.C.(*controller.Controller).Run = dispatch.push
	hub := app.EnsureDesktopWorkspaces()
	t.Cleanup(hub.Close)
	return app, hub, dispatch
}

func waitDesktopWorkspace(t *testing.T, hub *desktopui.DesktopWorkspaces, dispatch *testDispatch) {
	t.Helper()
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); time.Sleep(5 * time.Millisecond) {
		dispatch.drain()
		if !hub.Busy {
			return
		}
	}
	t.Fatal("workspace connection did not finish")
}

func workspaceState(name string) appmodel.SvcState {
	return appmodel.SvcState{ReferenceSource: "host-" + name, Cwd: "/same/project", Projects: []appmodel.Project{{Name: name, Path: "/same/project"}}, Threads: []appmodel.Thread{{ID: 7, ServerID: "thread-" + name, Title: name, Loaded: true, HistoryLoaded: true}}, Locale: "en", Ready: true, ProjectThreads: map[string][]appmodel.Thread{}}
}

func TestDesktopWorkspaceSwitchPreservesDraftsAndPanels(t *testing.T) {
	local, hub, dispatch := desktopWorkspaceFixture(t)
	local.C.(*controller.Controller).SetThreads([]appmodel.Thread{{ID: 7, ServerID: "local-thread", Title: "local", Loaded: true}})
	local.C.(*controller.Controller).SetActive(7)
	local.C.(*controller.Controller).RememberDraft("local draft")
	local.C.(*controller.Controller).SetAttachments([]string{"/local/photo.png"})
	localPanel := local.Panel()
	localPanel.Selected = "unsaved-local.txt"
	link := testDesktopLink()
	next := hub.Store.Clone()
	item, err := next.Remember(link, "prod-us")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	hub.Connect = func(ctx context.Context, p appmodel.DesktopWorkspace) (*appmodel.ServiceClient, appmodel.SvcState, error) {
		calls++
		cl, _ := restartTestClient(t, appmodel.SvcHello{Version: appmodel.ServiceVersion}, "")
		return cl, workspaceState("remote"), nil
	}
	hub.ConnectWorkspace(item)
	waitDesktopWorkspace(t, hub, dispatch)
	remoteApp := hub.Active
	if remoteApp == local || hub.Store.Active != item.ID || !remoteApp.C.(*controller.Controller).RemoteHost() || calls != 1 {
		t.Fatal("workspace did not switch in place", hub.Err)
	}
	if remoteApp.C.(*controller.Controller).Draft() != "" || len(remoteApp.C.(*controller.Controller).Attachments()) != 0 || remoteApp.Panel() == localPanel {
		t.Fatal("local state crossed into server workspace")
	}
	remoteApp.C.(*controller.Controller).SetActive(7)
	remoteApp.C.(*controller.Controller).RememberDraft("remote draft")
	remoteApp.C.(*controller.Controller).SetAttachments([]string{"miya-server:/remote/photo.png"})
	remotePanel := remoteApp.Panel()
	remotePanel.Selected = "remote-edit.txt"
	hub.SelectWorkspace(appmodel.LocalDesktopWorkspace)
	if hub.Active != local || local.C.(*controller.Controller).Active() != 7 || local.C.(*controller.Controller).Draft() != "local draft" || local.Panel() != localPanel || len(local.C.(*controller.Controller).Attachments()) != 1 {
		t.Fatal("returning to local lost draft, attachments or panel")
	}
	if !local.C.(*controller.Controller).WorkspaceVisible() || remoteApp.C.(*controller.Controller).WorkspaceVisible() {
		t.Fatal("inactive workspace incorrectly reports foreground presence")
	}
	remoteApp.C.(*controller.Controller).Mirror.SetLocale("zh")
	if appmodel.Tr("Projects") != "Projects" {
		t.Fatal("inactive server changed active workspace language")
	}
	hub.SelectWorkspace(item.ID)
	if hub.Active != remoteApp || remoteApp.C.(*controller.Controller).Active() != 7 || remoteApp.C.(*controller.Controller).Draft() != "remote draft" || remoteApp.Panel() != remotePanel || remotePanel.Selected != "remote-edit.txt" || calls != 1 {
		t.Fatal("server state was rebuilt instead of retained")
	}
	local.Section = "Settings"
	local.C.(*controller.Controller).RevealWorkspace()
	if hub.Active != local || local.Section != "" {
		t.Fatal("notification did not reveal its own workspace")
	}
}

func TestDesktopWorkspaceFailureKeepsCurrentContext(t *testing.T) {
	local, hub, dispatch := desktopWorkspaceFixture(t)
	local.C.(*controller.Controller).RememberDraft("do not lose")
	next := hub.Store.Clone()
	item, _ := next.Remember(testDesktopLink(), "offline")
	hub.Connect = func(context.Context, appmodel.DesktopWorkspace) (*appmodel.ServiceClient, appmodel.SvcState, error) {
		return nil, appmodel.SvcState{}, errors.New("offline")
	}
	hub.ConnectWorkspace(item)
	waitDesktopWorkspace(t, hub, dispatch)
	if hub.Active != local || hub.Store.Active != appmodel.LocalDesktopWorkspace || local.C.(*controller.Controller).Draft() != "do not lose" || hub.Err != "offline" || len(hub.Store.Items) != 1 {
		t.Fatal("failed connection replaced or persisted the active workspace")
	}
}

func TestDesktopWorkspaceCancelDiscardsLateConnection(t *testing.T) {
	local, hub, dispatch := desktopWorkspaceFixture(t)
	next := hub.Store.Clone()
	item, _ := next.Remember(testDesktopLink(), "slow")
	cl, _ := restartTestClient(t, appmodel.SvcHello{Version: appmodel.ServiceVersion}, "")
	started, release, dispatched := make(chan struct{}), make(chan struct{}), make(chan struct{})
	local.C.(*controller.Controller).Run = func(f func()) {
		dispatch.push(f)
		select {
		case <-dispatched:
		default:
			close(dispatched)
		}
	}
	hub.Connect = func(context.Context, appmodel.DesktopWorkspace) (*appmodel.ServiceClient, appmodel.SvcState, error) {
		close(started)
		<-release
		return cl, workspaceState("late"), nil
	}
	hub.ConnectWorkspace(item)
	<-started
	hub.CancelConnection()
	close(release)
	select {
	case <-dispatched:
	case <-time.After(3 * time.Second):
		t.Fatal("late connection did not return")
	}
	dispatch.drain()
	if hub.Active != local || len(hub.Sessions) != 1 || hub.Store.Active != appmodel.LocalDesktopWorkspace {
		t.Fatal("canceled result changed workspace")
	}
	select {
	case <-cl.Done():
	default:
		t.Fatal("canceled connection leaked a transport")
	}
}

func TestDesktopWorkspaceSidebarFlow(t *testing.T) {
	app, hub, _ := desktopWorkspaceFixture(t)
	tt := ui.NewTester(app.Render, 1180, 820)
	switcher, ok := tt.Find("Switch workspace")
	if !ok {
		t.Fatal("workspace switcher missing from primary navigation")
	}
	settings, ok := tt.Find("Settings")
	if !ok || switcher.Y < 700 || switcher.X <= settings.X || settings.Y < switcher.Y || settings.Y+settings.H > switcher.Y+switcher.H {
		t.Fatal("workspace switcher is not in the old footer shortcut slot", switcher, settings)
	}
	if _, ok := tt.Find("快捷键"); ok || tt.HasText("⌘") {
		t.Fatal("old command icon remains in sidebar")
	}
	if err := tt.Click("Switch workspace"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Add remote workspace…"); err != nil {
		t.Fatal(err)
	}
	if !hub.Open || !tt.HasText("Add remote workspace") || !tt.HasText("Workspace name") || !tt.HasText("Connection code") {
		t.Fatal("adding a workspace still requires settings")
	}
	saveUsageImage(t, tt, "desktop-workspace-add")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if hub.Open {
		t.Fatal("workspace dialog did not close")
	}
	app.Section = "Settings"
	tt.Frame()
	if tt.HasText("运行位置") || tt.HasText("切回本机") {
		t.Fatal("old settings-based connection flow remains")
	}
}

func TestDesktopWorkspaceSidebarRemoteSnapshot(t *testing.T) {
	app, hub, _ := desktopWorkspaceFixture(t)
	item, err := hub.Store.Remember(testDesktopLink(), "prod-us")
	if err != nil {
		t.Fatal(err)
	}
	hub.Store.Active = item.ID
	app.C.(*controller.Controller).SetProjects([]appmodel.Project{{Name: "miya-server", Path: "/srv/miya"}})
	app.C.(*controller.Controller).Cwd = "/srv/miya"
	app.Workspace, app.ProjectName, app.ExpandedProject = app.C.(*controller.Controller).Cwd, "miya-server", app.C.(*controller.Controller).Cwd
	tt := ui.NewTester(app.Render, 1180, 820)
	if !tt.HasText("prod-us") || !tt.HasText("miya-server") {
		t.Fatal("workspace and its project hierarchy missing", strings.Join(tt.Texts(), ","))
	}
	saveUsageImage(t, tt, "desktop-workspace-server")
	if err := tt.Click("Switch workspace"); err != nil {
		t.Fatal(err)
	}
	saveUsageImage(t, tt, "desktop-workspace-menu")
}
