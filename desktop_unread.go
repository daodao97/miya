package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

type desktopRead struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

// Backend session IDs survive history reloads and reassigned sidebar IDs.
func desktopUnreadKey(t Thread) string {
	return (taskNotice{Thread: strconv.Itoa(t.ID), ServerID: t.ServerID, Agent: threadAgent(t)}).unreadKey()
}

func (c *chatController) loadDesktopUnread() {
	c.desktopUnread = map[string]int64{}
	data, err := os.ReadFile(filepath.Join(serviceDir(), "desktop-unread.json"))
	if err == nil {
		_ = json.Unmarshal(data, &c.desktopUnread)
	}
	if c.desktopUnread == nil {
		c.desktopUnread = map[string]int64{}
	}
}

func (c *chatController) persistDesktopUnread() {
	data, err := json.Marshal(c.desktopUnread)
	if err != nil {
		return
	}
	path := filepath.Join(serviceDir(), "desktop-unread.json")
	if os.MkdirAll(filepath.Dir(path), 0700) == nil && os.WriteFile(path+".tmp", data, 0600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func (c *chatController) recordDesktopCompletion(old, next Thread) {
	if c.mirror != nil || c.desktopUnread == nil || old.Loading {
		return
	}
	if _, completed := completionNotice(old, next); !completed {
		return
	}
	key := desktopUnreadKey(next)
	if next.CompletedAtMs > c.desktopUnread[key] {
		c.desktopUnread[key] = next.CompletedAtMs
		c.persistDesktopUnread()
	}
}

func (c *chatController) desktopThreadUnread(t Thread) bool {
	key := desktopUnreadKey(t)
	version := c.desktopUnread[key]
	return version > 0 && c.desktopReadPending[key] < version
}

func (c *chatController) readDesktopThread(t Thread) {
	if !c.desktopThreadUnread(t) || t.Busy || t.Loading {
		return
	}
	key := desktopUnreadKey(t)
	read := desktopRead{Key: key, Version: c.desktopUnread[key]}
	if c.remote != nil {
		if c.desktopReadPending == nil {
			c.desktopReadPending = map[string]int64{}
		}
		c.desktopReadPending[key] = read.Version
		c.forward("desktop-read", read)
		return
	}
	c.markDesktopRead(read)
}

func (c *chatController) markDesktopRead(read desktopRead) {
	// A delayed acknowledgement must not clear a newer completed turn.
	if version := c.desktopUnread[read.Key]; version > 0 && read.Version >= version {
		delete(c.desktopUnread, read.Key)
		c.persistDesktopUnread()
	}
}

// Only a user action after completion can acknowledge that result. Merely
// repainting the conversation that was already open must preserve unread.
func (app *chatApp) requestDesktopRead(t Thread) {
	app.desktopReadIntent = desktopRead{}
	if !app.c.desktopThreadUnread(t) || t.Busy && !t.Loading {
		return
	}
	key := desktopUnreadKey(t)
	app.desktopReadIntent = desktopRead{Key: key, Version: app.c.desktopUnread[key]}
}

func (app *chatApp) markVisibleThreadRead() {
	w := currentWindow()
	if w == nil || !w.IsFocused() || w.IsMinimized() {
		return
	}
	app.markRequestedThreadRead()
}

func (app *chatApp) markRequestedThreadRead() {
	if app.section != "" || app.desktopReadIntent.Key == "" {
		return
	}
	t := app.c.current()
	read := app.desktopReadIntent
	if desktopUnreadKey(t) != read.Key || t.Busy || t.Loading || !t.HistoryLoaded {
		return
	}
	if app.c.mirror != nil {
		if _, received := app.c.mirror.messages[t.ID]; !received {
			return
		}
	}
	app.desktopReadIntent = desktopRead{}
	// A new turn may have completed while the chosen history was loading.
	if app.c.desktopUnread[read.Key] == read.Version {
		app.c.readDesktopThread(t)
	}
}
