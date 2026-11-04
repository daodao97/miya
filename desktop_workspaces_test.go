//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

func TestDesktopWorkspacesMigrateAndKeepLocalSelection(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	link := testDesktopLink()
	legacy, _ := json.Marshal(map[string]string{"link": link})
	if err := os.WriteFile(filepath.Join(serviceDir(), "remote-client.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := loadDesktopWorkspaces()
	if err != nil || len(store.Items) != 2 || store.current().Link != link {
		t.Fatal("legacy workspace did not migrate", err)
	}
	addr, _ := remote.ParseLink(link)
	item, err := store.remember(string(addr), "prod-us")
	if err != nil || len(store.Items) != 2 || item.Name != "prod-us" {
		t.Fatal("same pairing created multiple workspaces", err)
	}
	store.Active = localDesktopWorkspace
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDesktopWorkspaces()
	if err != nil || loaded.Active != localDesktopWorkspace || len(loaded.Items) != 2 || loaded.Items[1].Name != "prod-us" {
		t.Fatal("local selection or saved name lost", err)
	}
	if remoteServerLink() != "" {
		t.Fatal("legacy target resurrected after selecting local")
	}
	info, err := os.Stat(remoteServerPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("workspace capabilities are not private", err)
	}
}

func TestDesktopWorkspacesRejectCorruptRegistry(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	data := []byte(`{"version":99,"items":[]}`)
	if err := os.WriteFile(remoteServerPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDesktopWorkspaces(); err == nil {
		t.Fatal("unknown registry version silently accepted")
	}
	if err := saveRemoteServer(testDesktopLink()); err == nil {
		t.Fatal("corrupt registry overwritten by adding a workspace")
	}
	after, _ := os.ReadFile(remoteServerPath())
	if string(after) != string(data) {
		t.Fatal("failed migration changed saved capabilities")
	}
}

func desktopWorkspaceFixture(t *testing.T) (*chatApp, *desktopWorkspaces, *testDispatch) {
	t.Helper()
	t.Setenv("MIYA_DIR", t.TempDir())
	app, _ := testApp(t)
	dispatch := &testDispatch{}
	app.c.run = dispatch.push
	hub := app.ensureDesktopWorkspaces()
	t.Cleanup(hub.close)
	return app, hub, dispatch
}

func waitDesktopWorkspace(t *testing.T, hub *desktopWorkspaces, dispatch *testDispatch) {
	t.Helper()
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); time.Sleep(5 * time.Millisecond) {
		dispatch.drain()
		if !hub.busy {
			return
		}
	}
	t.Fatal("workspace connection did not finish")
}

func workspaceState(name string) svcState {
	return svcState{ReferenceSource: "host-" + name, Cwd: "/same/project", Projects: []Project{{Name: name, Path: "/same/project"}}, Threads: []Thread{{ID: 7, ServerID: "thread-" + name, Title: name, Loaded: true, HistoryLoaded: true}}, Locale: "en", Ready: true, ProjectThreads: map[string][]Thread{}}
}

func TestDesktopWorkspaceSwitchPreservesDraftsAndPanels(t *testing.T) {
	local, hub, dispatch := desktopWorkspaceFixture(t)
	local.c.setThreads([]Thread{{ID: 7, ServerID: "local-thread", Title: "local", Loaded: true}})
	local.c.setActive(7)
	local.c.rememberDraft("local draft")
	local.c.setAttachments([]string{"/local/photo.png"})
	localPanel := local.panel()
	localPanel.selected = "unsaved-local.txt"
	link := testDesktopLink()
	next := hub.store.clone()
	item, err := next.remember(link, "prod-us")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	hub.connect = func(ctx context.Context, p desktopWorkspace) (*serviceClient, svcState, error) {
		calls++
		cl, _ := restartTestClient(t, svcHello{Version: serviceVersion}, "")
		return cl, workspaceState("remote"), nil
	}
	hub.connectWorkspace(item)
	waitDesktopWorkspace(t, hub, dispatch)
	remoteApp := hub.active
	if remoteApp == local || hub.store.Active != item.ID || !remoteApp.c.remoteHost() || calls != 1 {
		t.Fatal("workspace did not switch in place", hub.err)
	}
	if remoteApp.c.draft() != "" || len(remoteApp.c.attachments()) != 0 || remoteApp.panel() == localPanel {
		t.Fatal("local state crossed into server workspace")
	}
	remoteApp.c.setActive(7)
	remoteApp.c.rememberDraft("remote draft")
	remoteApp.c.setAttachments([]string{"miya-server:/remote/photo.png"})
	remotePanel := remoteApp.panel()
	remotePanel.selected = "remote-edit.txt"
	hub.selectWorkspace(localDesktopWorkspace)
	if hub.active != local || local.c.active() != 7 || local.c.draft() != "local draft" || local.panel() != localPanel || len(local.c.attachments()) != 1 {
		t.Fatal("returning to local lost draft, attachments or panel")
	}
	if !local.c.workspaceVisible() || remoteApp.c.workspaceVisible() {
		t.Fatal("inactive workspace incorrectly reports foreground presence")
	}
	remoteApp.c.mirror.setLocale("zh")
	if tr("Projects") != "Projects" {
		t.Fatal("inactive server changed active workspace language")
	}
	hub.selectWorkspace(item.ID)
	if hub.active != remoteApp || remoteApp.c.active() != 7 || remoteApp.c.draft() != "remote draft" || remoteApp.panel() != remotePanel || remotePanel.selected != "remote-edit.txt" || calls != 1 {
		t.Fatal("server state was rebuilt instead of retained")
	}
	local.section = "Settings"
	local.c.revealWorkspace()
	if hub.active != local || local.section != "" {
		t.Fatal("notification did not reveal its own workspace")
	}
}

func TestDesktopWorkspaceFailureKeepsCurrentContext(t *testing.T) {
	local, hub, dispatch := desktopWorkspaceFixture(t)
	local.c.rememberDraft("do not lose")
	next := hub.store.clone()
	item, _ := next.remember(testDesktopLink(), "offline")
	hub.connect = func(context.Context, desktopWorkspace) (*serviceClient, svcState, error) {
		return nil, svcState{}, errors.New("offline")
	}
	hub.connectWorkspace(item)
	waitDesktopWorkspace(t, hub, dispatch)
	if hub.active != local || hub.store.Active != localDesktopWorkspace || local.c.draft() != "do not lose" || hub.err != "offline" || len(hub.store.Items) != 1 {
		t.Fatal("failed connection replaced or persisted the active workspace")
	}
}

func TestDesktopWorkspaceCancelDiscardsLateConnection(t *testing.T) {
	local, hub, dispatch := desktopWorkspaceFixture(t)
	next := hub.store.clone()
	item, _ := next.remember(testDesktopLink(), "slow")
	cl, _ := restartTestClient(t, svcHello{Version: serviceVersion}, "")
	started, release, dispatched := make(chan struct{}), make(chan struct{}), make(chan struct{})
	local.c.run = func(f func()) {
		dispatch.push(f)
		select {
		case <-dispatched:
		default:
			close(dispatched)
		}
	}
	hub.connect = func(context.Context, desktopWorkspace) (*serviceClient, svcState, error) {
		close(started)
		<-release
		return cl, workspaceState("late"), nil
	}
	hub.connectWorkspace(item)
	<-started
	hub.cancelConnection()
	close(release)
	select {
	case <-dispatched:
	case <-time.After(3 * time.Second):
		t.Fatal("late connection did not return")
	}
	dispatch.drain()
	if hub.active != local || len(hub.sessions) != 1 || hub.store.Active != localDesktopWorkspace {
		t.Fatal("canceled result changed workspace")
	}
	select {
	case <-cl.closed:
	default:
		t.Fatal("canceled connection leaked a transport")
	}
}

func TestDesktopWorkspaceSidebarFlow(t *testing.T) {
	app, hub, _ := desktopWorkspaceFixture(t)
	tt := ui.NewTester(app.view, 1180, 820)
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
	if !hub.open || !tt.HasText("Add remote workspace") || !tt.HasText("Workspace name") || !tt.HasText("Connection code") {
		t.Fatal("adding a workspace still requires settings")
	}
	saveUsageImage(t, tt, "desktop-workspace-add")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if hub.open {
		t.Fatal("workspace dialog did not close")
	}
	app.section = "Settings"
	tt.Frame()
	if tt.HasText("运行位置") || tt.HasText("切回本机") {
		t.Fatal("old settings-based connection flow remains")
	}
}

func TestDesktopWorkspaceSidebarRemoteSnapshot(t *testing.T) {
	app, hub, _ := desktopWorkspaceFixture(t)
	item, err := hub.store.remember(testDesktopLink(), "prod-us")
	if err != nil {
		t.Fatal(err)
	}
	hub.store.Active = item.ID
	app.c.setProjects([]Project{{Name: "miya-server", Path: "/srv/miya"}})
	app.c.cwd = "/srv/miya"
	app.workspace, app.projectName, app.expandedProject = app.c.cwd, "miya-server", app.c.cwd
	tt := ui.NewTester(app.view, 1180, 820)
	if !tt.HasText("prod-us") || !tt.HasText("miya-server") {
		t.Fatal("workspace and its project hierarchy missing", strings.Join(tt.Texts(), ","))
	}
	saveUsageImage(t, tt, "desktop-workspace-server")
	if err := tt.Click("Switch workspace"); err != nil {
		t.Fatal(err)
	}
	saveUsageImage(t, tt, "desktop-workspace-menu")
}
