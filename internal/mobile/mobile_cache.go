package mobile

import (
	appmodel "miya/internal/appmodel"
	remote "miya/internal/remote"
	uiruntime "miya/internal/uiruntime"
	os "os"
	filepath "path/filepath"
	sync "sync"
	time "time"
)

type mobileCacheWork struct {
	mu      sync.Mutex
	pending sync.WaitGroup
}

// openDiskCache uses the directory of the desktop at link, showing what it
// last saw of the desktop until the desktop answers.
func (m *App) OpenDiskCache(link string) {
	m.FlushCache(true)
	dir := ""
	if m.DataDir != "" && link != "" {
		dir = filepath.Join(m.DataDir, "threads", appmodel.CacheName(link))
	}
	if dir != m.CacheDir {
		m.MsgCache, m.msgKeys = nil, nil
	}
	m.CacheDir = dir
	if dir == "" || len(m.State.Projects) > 0 {
		return
	}
	var saved appmodel.PhoneSavedState
	if appmodel.ReadGzipJSON(filepath.Join(dir, appmodel.StateFile), &saved) == nil {
		// Approvals may have been answered since; the desktop sends them.
		saved.State.Approvals = nil
		m.State = saved.State
		if m.Host == "" {
			m.Host = saved.Host
		}
	}
}

// dropDiskCache forgets the current desktop's conversations.
func (m *App) DropDiskCache() {
	if m.flushTimer != nil {
		m.flushTimer.Stop()
		m.flushTimer = nil
	}
	dir := m.CacheDir
	m.Dirty, m.CacheDir, m.MsgCache, m.msgKeys = nil, "", nil, nil
	m.cacheWork.pending.Wait()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

func (m *App) loadDisk(serverID string) []appmodel.Message {
	if m.CacheDir == "" || serverID == "" {
		return nil
	}
	var msgs []appmodel.Message
	if appmodel.ReadGzipJSON(filepath.Join(m.CacheDir, appmodel.CacheName(serverID)+".json.gz"), &msgs) != nil {
		return nil
	}
	return msgs
}

// persist saves v under key shortly, with whatever else changed meanwhile.
func (m *App) persist(key string, v any) {
	if m.CacheDir == "" {
		return
	}
	if m.Dirty == nil {
		m.Dirty = map[string]any{}
	}
	m.Dirty[key] = v
	if m.flushTimer == nil {
		m.flushTimer = time.AfterFunc(appmodel.CacheFlushDelay, func() { uiruntime.RunOnMain(func() { m.FlushCache(false) }) })
	}
}

func (m *App) PersistState() {
	m.persist(appmodel.StateKey, appmodel.PhoneSavedState{Host: m.Host, State: m.State})
}

// flushCache writes what changed, in the background unless wait is set, as
// when iOS is about to suspend the app.
func (m *App) FlushCache(wait bool) {
	if m.flushTimer != nil {
		m.flushTimer.Stop()
		m.flushTimer = nil
	}
	dir, dirty := m.CacheDir, m.Dirty
	m.Dirty = nil
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
			name := appmodel.StateFile
			if key != appmodel.StateKey {
				name = appmodel.CacheName(key) + ".json.gz"
			}
			appmodel.WriteGzipJSON(filepath.Join(dir, name), v)
		}
		appmodel.PruneCache(dir, appmodel.PhoneDiskThreads)
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
func (m *App) SubscribeRequestFor(serverID string, id int) (r remote.Request) {
	r = m.SubscribeRequest(id)
	if r.ServerID != serverID {
		r.ServerID, r.Project, r.Agent, r.Have = serverID, "", "", nil
	}
	if len(r.Have) == 0 {
		for _, msg := range m.CachedMessages(serverID) {
			r.Have = append(r.Have, remote.Seen{ID: msg.ID, FP: appmodel.MessageFingerprint(msg)})
		}
	}
	return r
}
