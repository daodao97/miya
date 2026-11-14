//go:build !mygo_ui && !windows

package integration

import (
	"encoding/json"
	"miya/internal/appdir"
	"miya/internal/chat/model"
	projectmodel "miya/internal/project/model"
	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
	"miya/internal/service/workspaces"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopWorkspacesMigrateAndKeepLocalSelection(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	link := testDesktopLink()
	legacy, _ := json.Marshal(map[string]string{"link": link})
	if err := os.WriteFile(filepath.Join(appdir.Dir(), "remote-client.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := workspaces.Load()
	if err != nil || len(store.Items) != 2 || store.Current().Link != link {
		t.Fatal("legacy workspace did not migrate", err)
	}
	addr, _ := remote.ParseLink(link)
	item, err := store.Remember(string(addr), "prod-us")
	if err != nil || len(store.Items) != 2 || item.Name != "prod-us" {
		t.Fatal("same pairing created multiple workspaces", err)
	}
	store.Active = workspaces.Local
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := workspaces.Load()
	if err != nil || loaded.Active != workspaces.Local || len(loaded.Items) != 2 || loaded.Items[1].Name != "prod-us" {
		t.Fatal("local selection or saved name lost", err)
	}
	if workspaces.RemoteServerLink() != "" {
		t.Fatal("legacy target resurrected after selecting local")
	}
	info, err := os.Stat(workspaces.RemoteServerPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("workspace capabilities are not private", err)
	}
}

func TestDesktopWorkspacesRejectCorruptRegistry(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	data := []byte(`{"version":99,"items":[]}`)
	if err := os.WriteFile(workspaces.RemoteServerPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaces.Load(); err == nil {
		t.Fatal("unknown registry version silently accepted")
	}
	if err := workspaces.SaveRemoteServer(testDesktopLink()); err == nil {
		t.Fatal("corrupt registry overwritten by adding a workspace")
	}
	after, _ := os.ReadFile(workspaces.RemoteServerPath())
	if string(after) != string(data) {
		t.Fatal("failed migration changed saved capabilities")
	}
}

func workspaceState(name string) svcproto.State {
	return svcproto.State{ReferenceSource: "host-" + name, Cwd: "/same/project", Projects: []projectmodel.Project{{Name: name, Path: "/same/project"}}, Threads: []model.Thread{{ID: 7, ServerID: "thread-" + name, Title: name, Loaded: true, HistoryLoaded: true}}, Locale: "en", Ready: true, ProjectThreads: map[string][]model.Thread{}}
}
