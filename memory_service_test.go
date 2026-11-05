//go:build !windows

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/memory"
	"miya/internal/memory/manager"
)

func TestMemoryServiceSharesPersistenceAndInvalidationAcrossWindows(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	f := newServiceFixture(t)
	a, b := f.window(), f.window()
	t.Cleanup(func() { a.remote.Close(); b.remote.Close() })
	var saved memory.Entry
	if err := a.remote.Call("memorySave", manager.Request{Kind: "fact", Content: "窗口共享的临时协议约定"}, &saved); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("memory revision in both windows", func() bool { return a.memoryRevision > 0 && a.memoryRevision == b.memoryRevision })
	var rev uint64
	f.win.Do(func() {
		rev = b.memoryRevision
		if a.memories().Store != nil || b.memories().Store != nil {
			t.Error("window owns a memory writer")
		}
	})
	var list manager.List
	if err := b.remote.Call("memoryList", manager.Request{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].ID != saved.ID {
		t.Fatal("other window did not share memory")
	}
	var state svcState
	if err := a.remote.Call("state", nil, &state); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), saved.Content) {
		t.Fatal("library body leaked into state broadcast")
	}
	if err := a.remote.Call("memoryForget", manager.Request{ID: saved.ID, Version: saved.Version}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("forget invalidation in the other window", func() bool { return b.memoryRevision > rev })
	if err := b.remote.Call("memoryGet", manager.Request{ID: saved.ID, Version: saved.Version}, &saved); err != nil {
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
	t.Cleanup(func() { a.remote.Close(); b.remote.Close() })
	settings := memory.Settings{AutoInject: true, AutoExtract: true}
	if err := a.remote.Call("memoryConfigure", manager.Request{Settings: &settings}, nil); err != nil {
		t.Fatal(err)
	}
	var list manager.List
	if err := b.remote.Call("memoryList", manager.Request{}, &list); err != nil || !list.Settings.AutoInject || !list.Settings.AutoExtract || !list.ExtractorAvailable {
		t.Fatalf("automatic settings not shared: %+v %v", list, err)
	}
	f.serv.Do(func() { f.srv.c.setThreads([]Thread{{ID: 1, ServerID: "session", HistoryLoaded: true}}) })
	disabled := true
	if err := a.remote.Call("memorySessionConfigure", manager.Request{Thread: 1, SessionDisabled: &disabled}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("session memory setting", func() bool { return len(b.threads()) > 0 && b.threads()[0].MemoryDisabled })
	var state svcState
	if err := a.remote.Call("state", nil, &state); err != nil || len(state.Threads) != 1 || !state.Threads[0].MemoryDisabled {
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
	c := &chatController{cwd: "/"}
	if _, err := c.memories().Scope(manager.Request{}); err == nil {
		t.Fatal("memory settings were scoped to the filesystem root")
	}
	if scope, err := c.memories().Scope(manager.Request{Scope: "user"}); err != nil || scope.Kind != "user" {
		t.Fatalf("user preferences must stay available without a project: %v %v", scope, err)
	}
}
