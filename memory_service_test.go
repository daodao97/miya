//go:build !windows

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/memory"
)

func TestMemoryServiceSharesPersistenceAndInvalidationAcrossWindows(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	f := newServiceFixture(t)
	a, b := f.window(), f.window()
	t.Cleanup(func() { a.remote.Close(); b.remote.Close() })
	var saved memory.Entry
	if err := a.remote.call("memorySave", memoryRequest{Kind: "fact", Content: "窗口共享的临时协议约定"}, &saved); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("memory revision in both windows", func() bool { return a.memoryRevision > 0 && a.memoryRevision == b.memoryRevision })
	var rev uint64
	f.win.do(func() {
		rev = b.memoryRevision
		if a.memory != nil || b.memory != nil {
			t.Error("window owns a memory writer")
		}
	})
	var list memoryList
	if err := b.remote.call("memoryList", memoryRequest{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].ID != saved.ID {
		t.Fatal("other window did not share memory")
	}
	var state svcState
	if err := a.remote.call("state", nil, &state); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), saved.Content) {
		t.Fatal("library body leaked into state broadcast")
	}
	if err := a.remote.call("memoryForget", memoryRequest{ID: saved.ID, Version: saved.Version}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("forget invalidation in the other window", func() bool { return b.memoryRevision > rev })
	if err := b.remote.call("memoryGet", memoryRequest{ID: saved.ID, Version: saved.Version}, &saved); err != nil {
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
	if err := a.remote.call("memoryConfigure", memoryRequest{Settings: &settings}, nil); err != nil {
		t.Fatal(err)
	}
	var list memoryList
	if err := b.remote.call("memoryList", memoryRequest{}, &list); err != nil || !list.Settings.AutoInject || !list.Settings.AutoExtract || !list.ExtractorAvailable {
		t.Fatalf("automatic settings not shared: %+v %v", list, err)
	}
	f.serv.do(func() { f.srv.c.setThreads([]Thread{{ID: 1, ServerID: "session", HistoryLoaded: true}}) })
	disabled := true
	if err := a.remote.call("memorySessionConfigure", memoryRequest{Thread: 1, SessionDisabled: &disabled}, nil); err != nil {
		t.Fatal(err)
	}
	f.waitWindow("session memory setting", func() bool { return len(b.threads()) > 0 && b.threads()[0].MemoryDisabled })
	var state svcState
	if err := a.remote.call("state", nil, &state); err != nil || len(state.Threads) != 1 || !state.Threads[0].MemoryDisabled {
		t.Fatalf("session state not visible: %+v %v", state.Threads, err)
	}
}
