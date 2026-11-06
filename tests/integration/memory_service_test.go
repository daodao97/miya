//go:build !windows

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	svcproto "miya/internal/service/protocol"
)

func TestMemoryServiceSharesPersistenceAndInvalidationAcrossWindows(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	f := newServiceFixture(t)
	a, b := f.window(), f.window()
	t.Cleanup(func() { a.Remote.Close(); b.Remote.Close() })
	var saved memory.Entry
	if err := a.Remote.Call("memorySave", manager.Request{Kind: "fact", Content: "窗口共享的临时协议约定"}, &saved); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("memory revision in both windows", func() bool { return a.MemoryRevision > 0 && a.MemoryRevision == b.MemoryRevision })
	var rev uint64
	f.win.Do(func() {
		rev = b.MemoryRevision
		if a.Memories().Store != nil || b.Memories().Store != nil {
			t.Error("window owns a memory writer")
		}
	})
	var list manager.List
	if err := b.Remote.Call("memoryList", manager.Request{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].ID != saved.ID {
		t.Fatal("other window did not share memory")
	}
	var state svcproto.State
	if err := a.Remote.Call("state", nil, &state); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), saved.Content) {
		t.Fatal("library body leaked into state broadcast")
	}
	if err := a.Remote.Call("memoryForget", manager.Request{ID: saved.ID, Version: saved.Version}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("forget invalidation in the other window", func() bool { return b.MemoryRevision > rev })
	if err := b.Remote.Call("memoryGet", manager.Request{ID: saved.ID, Version: saved.Version}, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Content != "" || len(saved.Sources) != 0 || saved.State != "forgotten" {
		t.Fatalf("other window retained forgotten memory: %+v", saved)
	}
}

func TestMemoryAutomaticSettingsAndSessionDisableThroughService(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	f := newServiceFixture(t)
	a, b := f.window(), f.window()
	t.Cleanup(func() { a.Remote.Close(); b.Remote.Close() })
	settings := memory.Settings{AutoInject: true, AutoExtract: true}
	if err := a.Remote.Call("memoryConfigure", manager.Request{Settings: &settings}, nil); err != nil {
		t.Fatal(err)
	}
	var list manager.List
	if err := b.Remote.Call("memoryList", manager.Request{}, &list); err != nil || !list.Settings.AutoInject || !list.Settings.AutoExtract || !list.ExtractorAvailable {
		t.Fatalf("automatic settings not shared: %+v %v", list, err)
	}
	f.serv.Do(func() {
		f.C.SetThreads([]model.Thread{{ID: 1, ServerID: "session", HistoryLoaded: true}})
	})
	disabled := true
	if err := a.Remote.Call("memorySessionConfigure", manager.Request{Thread: 1, SessionDisabled: &disabled}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("session memory setting", func() bool { return len(b.Threads()) > 0 && b.Threads()[0].MemoryDisabled })
	var state svcproto.State
	if err := a.Remote.Call("state", nil, &state); err != nil || len(state.Threads) != 1 || !state.Threads[0].MemoryDisabled {
		t.Fatalf("session state not visible: %+v %v", state.Threads, err)
	}
}

func TestMemoryScopeRejectsFilesystemRoot(t *testing.T) {
	for _, root := range []string{"/", "", "  ", "//"} {
		if !manager.RootProject(root) {
			t.Fatalf("%q accepted as a project", root)
		}
	}
	if manager.RootProject("/Users/someone/project") {
		t.Fatal("a project folder was rejected")
	}
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: "/"}}
	if _, err := c.Memories().Scope(manager.Request{}); err == nil {
		t.Fatal("memory settings were scoped to the filesystem root")
	}
	if scope, err := c.Memories().Scope(manager.Request{Scope: "user"}); err != nil || scope.Kind != "user" {
		t.Fatalf("user preferences must stay available without a project: %v %v", scope, err)
	}
}
