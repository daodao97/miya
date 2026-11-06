package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	mobile "miya/internal/mobile"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
)

func TestMobileDiskCacheSurvivesRelaunch(t *testing.T) {
	data := t.TempDir()
	m := mobile.NewMobileApp()
	m.DataDir = data
	m.OpenDiskCache("miya://connect?a")
	m.Host = "mac"
	m.State = appmodel.PhoneState{Projects: []appmodel.PhoneProject{{Name: "Miya", Path: "/m", Current: true, Threads: []appmodel.Thread{{ID: 3, Title: "cached", ServerID: "s3"}}}}}
	m.State.Approvals = append(m.State.Approvals, m.State.Approvals...)
	m.PersistState()
	msgs := longThread(30).Messages
	m.Remember("s3", msgs)
	m.FlushCache(true)

	// A relaunched app, before the desktop answers.
	n := mobile.NewMobileApp()
	n.DataDir = data
	n.OpenDiskCache("miya://connect?a")
	if n.Host != "mac" || len(n.State.Projects) != 1 || n.State.Projects[0].Threads[0].Title != "cached" {
		t.Fatalf("the list should come back from disk: host %q, %+v", n.Host, n.State.Projects)
	}
	if got := n.CachedMessages("s3"); !sameMessages(got, msgs) {
		t.Fatalf("the messages should come back from disk: %d", len(got))
	}
	if req := n.SubscribeRequestFor("s3", 3); len(req.Have) != 30 {
		t.Errorf("subscribing should say what the phone holds: %d", len(req.Have))
	}

	// Another desktop sees none of it.
	o := mobile.NewMobileApp()
	o.DataDir = data
	o.OpenDiskCache("miya://connect?b")
	if len(o.State.Projects) != 0 || len(o.CachedMessages("s3")) != 0 {
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
	dir := n.CacheDir
	n.DropDiskCache()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the desktop's cache should be removed: %v", err)
	}
}

func TestMobileDiskCacheIsBounded(t *testing.T) {
	m := mobile.NewMobileApp()
	m.DataDir = t.TempDir()
	m.OpenDiskCache("miya://connect?a")
	for i := 0; i < appmodel.PhoneDiskThreads+8; i++ {
		m.Remember("thread-"+string(rune('A'+i%26))+string(rune('a'+i/26)), []appmodel.Message{{ID: 1, Content: "x"}})
	}
	m.PersistState()
	m.FlushCache(true)
	entries, _ := os.ReadDir(m.CacheDir)
	threads, state := 0, false
	for _, e := range entries {
		if e.Name() == appmodel.StateFile {
			state = true
		} else {
			threads++
		}
	}
	if threads > appmodel.PhoneDiskThreads || !state {
		t.Errorf("%d conversations kept (max %d), state kept %v", threads, appmodel.PhoneDiskThreads, state)
	}
	var back appmodel.PhoneSavedState
	if err := appmodel.ReadGzipJSON(filepath.Join(m.CacheDir, appmodel.StateFile), &back); err != nil {
		t.Fatal(err)
	}
	_ = json.Valid
}
