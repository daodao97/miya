package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMobileDiskCacheSurvivesRelaunch(t *testing.T) {
	data := t.TempDir()
	m := newMobileApp()
	m.dataDir = data
	m.openDiskCache("miya://connect?a")
	m.host = "mac"
	m.state = phoneState{Projects: []phoneProject{{Name: "Miya", Path: "/m", Current: true, Threads: []Thread{{ID: 3, Title: "cached", ServerID: "s3"}}}}}
	m.state.Approvals = append(m.state.Approvals, m.state.Approvals...)
	m.persistState()
	msgs := longThread(30).Messages
	m.remember("s3", msgs)
	m.flushCache(true)

	// A relaunched app, before the desktop answers.
	n := newMobileApp()
	n.dataDir = data
	n.openDiskCache("miya://connect?a")
	if n.host != "mac" || len(n.state.Projects) != 1 || n.state.Projects[0].Threads[0].Title != "cached" {
		t.Fatalf("the list should come back from disk: host %q, %+v", n.host, n.state.Projects)
	}
	if got := n.cachedMessages("s3"); !sameMessages(got, msgs) {
		t.Fatalf("the messages should come back from disk: %d", len(got))
	}
	if req := n.subscribeRequestFor("s3", 3); len(req.Have) != 30 {
		t.Errorf("subscribing should say what the phone holds: %d", len(req.Have))
	}

	// Another desktop sees none of it.
	o := newMobileApp()
	o.dataDir = data
	o.openDiskCache("miya://connect?b")
	if len(o.state.Projects) != 0 || len(o.cachedMessages("s3")) != 0 {
		t.Error("another desktop's cache should be separate")
	}

	// Nothing names the conversation in clear.
	filepath.Walk(data, func(path string, _ os.FileInfo, _ error) error {
		if strings.Contains(path, "s3") {
			t.Errorf("file names should not show conversation IDs: %s", path)
		}
		return nil
	})

	// Forgetting the desktop removes its conversations.
	dir := n.cacheDir
	n.dropDiskCache()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the desktop's cache should be removed: %v", err)
	}
}

func TestMobileDiskCacheIsBounded(t *testing.T) {
	m := newMobileApp()
	m.dataDir = t.TempDir()
	m.openDiskCache("miya://connect?a")
	for i := 0; i < phoneDiskThreads+8; i++ {
		m.remember("thread-"+string(rune('A'+i%26))+string(rune('a'+i/26)), []Message{{ID: 1, Content: "x"}})
	}
	m.persistState()
	m.flushCache(true)
	entries, _ := os.ReadDir(m.cacheDir)
	threads, state := 0, false
	for _, e := range entries {
		if e.Name() == stateFile {
			state = true
		} else {
			threads++
		}
	}
	if threads > phoneDiskThreads || !state {
		t.Errorf("%d conversations kept (max %d), state kept %v", threads, phoneDiskThreads, state)
	}
	var back phoneSavedState
	if err := readGzipJSON(filepath.Join(m.cacheDir, stateFile), &back); err != nil {
		t.Fatal(err)
	}
	_ = json.Valid
}
