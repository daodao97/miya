package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"miya/internal/remote"
)

type mobileCacheWork struct {
	mu      sync.Mutex
	pending sync.WaitGroup
}

// openDiskCache uses the directory of the desktop at link, showing what it
// last saw of the desktop until the desktop answers.
func (m *mobileApp) openDiskCache(link string) {
	m.flushCache(true)
	dir := ""
	if m.dataDir != "" && link != "" {
		dir = filepath.Join(m.dataDir, "threads", cacheName(link))
	}
	if dir != m.cacheDir {
		m.msgCache, m.msgKeys = nil, nil
	}
	m.cacheDir = dir
	if dir == "" || len(m.state.Projects) > 0 {
		return
	}
	var saved phoneSavedState
	if readGzipJSON(filepath.Join(dir, stateFile), &saved) == nil {
		// Approvals may have been answered since; the desktop sends them.
		saved.State.Approvals = nil
		m.state = saved.State
		if m.host == "" {
			m.host = saved.Host
		}
	}
}

// dropDiskCache forgets the current desktop's conversations.
func (m *mobileApp) dropDiskCache() {
	if m.flushTimer != nil {
		m.flushTimer.Stop()
		m.flushTimer = nil
	}
	dir := m.cacheDir
	m.dirty, m.cacheDir, m.msgCache, m.msgKeys = nil, "", nil, nil
	m.cacheWork.pending.Wait()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

func (m *mobileApp) loadDisk(serverID string) []Message {
	if m.cacheDir == "" || serverID == "" {
		return nil
	}
	var msgs []Message
	if readGzipJSON(filepath.Join(m.cacheDir, cacheName(serverID)+".json.gz"), &msgs) != nil {
		return nil
	}
	return msgs
}

// persist saves v under key shortly, with whatever else changed meanwhile.
func (m *mobileApp) persist(key string, v any) {
	if m.cacheDir == "" {
		return
	}
	if m.dirty == nil {
		m.dirty = map[string]any{}
	}
	m.dirty[key] = v
	if m.flushTimer == nil {
		m.flushTimer = time.AfterFunc(cacheFlushDelay, func() { runOnMain(func() { m.flushCache(false) }) })
	}
}

func (m *mobileApp) persistState() {
	m.persist(stateKey, phoneSavedState{Host: m.host, State: m.state})
}

// flushCache writes what changed, in the background unless wait is set, as
// when iOS is about to suspend the app.
func (m *mobileApp) flushCache(wait bool) {
	if m.flushTimer != nil {
		m.flushTimer.Stop()
		m.flushTimer = nil
	}
	dir, dirty := m.cacheDir, m.dirty
	m.dirty = nil
	if wait {
		m.cacheWork.pending.Wait()
	}
	if dir == "" || len(dirty) == 0 {
		return
	}
	write := func() {
		m.cacheWork.mu.Lock()
		defer m.cacheWork.mu.Unlock()
		for key, v := range dirty {
			name := stateFile
			if key != stateKey {
				name = cacheName(key) + ".json.gz"
			}
			writeGzipJSON(filepath.Join(dir, name), v)
		}
		pruneCache(dir, phoneDiskThreads)
	}
	if wait {
		write()
	} else {
		m.cacheWork.pending.Add(1)
		go func() {
			defer m.cacheWork.pending.Done()
			write()
		}()
	}
}

// subscribeRequestFor is subscribeRequest for a conversation known by its
// ServerID, as after a relaunch before the list arrives.
func (m *mobileApp) subscribeRequestFor(serverID string, id int) (r remote.Request) {
	r = m.subscribeRequest(id)
	if r.ServerID != serverID {
		r.ServerID, r.Project, r.Agent, r.Have = serverID, "", "", nil
	}
	if len(r.Have) == 0 {
		for _, msg := range m.cachedMessages(serverID) {
			r.Have = append(r.Have, remote.Seen{ID: msg.ID, FP: messageFingerprint(msg)})
		}
	}
	return r
}
