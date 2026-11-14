package client

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"miya/internal/chat/model"
	"miya/internal/fsutil"
	phonecache "miya/internal/phone/cache"
	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
	"miya/internal/service/workspaces"
)

type mobileCacheWork struct {
	mu      sync.Mutex
	pending sync.WaitGroup
}

func (m *Client) OpenDiskCache(link string) {
	m.FlushCache(true)
	dir := ""
	if m.DataDir != "" && link != "" {
		dir = filepath.Join(m.DataDir, "threads", workspaces.CacheName(link))
	}
	if dir != m.CacheDir {
		m.MsgCache, m.msgKeys = nil, nil
	}
	m.CacheDir = dir
	if dir == "" || len(m.State.Projects) > 0 {
		return
	}
	var saved phonecache.SavedState
	if fsutil.ReadGzipJSON(filepath.Join(dir, phonecache.StateFile), &saved) == nil {
		// Approvals may have been answered since; the desktop sends them.
		saved.State.Approvals = nil
		m.State = saved.State
		if m.Host == "" {
			m.Host = saved.Host
		}
	}
}

func (m *Client) DropDiskCache() {
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

func (m *Client) loadDisk(serverID string) []model.Message {
	if m.CacheDir == "" || serverID == "" {
		return nil
	}
	var msgs []model.Message
	if fsutil.ReadGzipJSON(filepath.Join(m.CacheDir, workspaces.CacheName(serverID)+".json.gz"), &msgs) != nil {
		return nil
	}
	return msgs
}

func (m *Client) persist(key string, v any) {
	if m.CacheDir == "" {
		return
	}
	if m.Dirty == nil {
		m.Dirty = map[string]any{}
	}
	m.Dirty[key] = v
	if m.flushTimer == nil {
		m.flushTimer = time.AfterFunc(phonecache.CacheFlushDelay, func() { m.post(func() { m.FlushCache(false) }) })
	}
}

func (m *Client) PersistState() {
	m.persist(phonecache.StateKey, phonecache.SavedState{Host: m.Host, State: m.State})
}

func (m *Client) FlushCache(wait bool) {
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
			name := phonecache.StateFile
			if key != phonecache.StateKey {
				name = workspaces.CacheName(key) + ".json.gz"
			}
			fsutil.WriteGzipJSON(filepath.Join(dir, name), v)
		}
		phonecache.Prune(dir, phonecache.PhoneDiskThreads)
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

func (m *Client) SubscribeRequestFor(serverID string, id int) (r remote.Request) {
	r = m.SubscribeRequest(id)
	if r.ServerID != serverID {
		r.ServerID, r.Project, r.Agent, r.Have = serverID, "", "", nil
	}
	if len(r.Have) == 0 {
		for _, msg := range m.CachedMessages(serverID) {
			r.Have = append(r.Have, remote.Seen{ID: msg.ID, FP: svcproto.MessageFingerprint(msg)})
		}
	}
	return r
}
