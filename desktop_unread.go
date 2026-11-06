package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"miya/internal/appdir"
	"miya/internal/model"
	"miya/internal/notification"
	"miya/internal/remote"
)

type desktopRead struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

// Backend session IDs survive history reloads and reassigned sidebar IDs.
func desktopUnreadKey(t Thread) string {
	return (notification.Notice{Thread: strconv.Itoa(t.ID), ServerID: t.ServerID, Agent: model.ThreadAgent(t)}).UnreadKey()
}

func (c *chatController) loadDesktopUnread() {
	c.desktopUnread = map[string]int64{}
	data, err := os.ReadFile(filepath.Join(appdir.Dir(), "desktop-unread.json"))
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
	path := filepath.Join(appdir.Dir(), "desktop-unread.json")
	if os.MkdirAll(filepath.Dir(path), 0700) == nil && os.WriteFile(path+".tmp", data, 0600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func (c *chatController) recordDesktopCompletion(old, next Thread) {
	if c.mirror != nil || c.desktopUnread == nil || old.Loading {
		return
	}
	if _, completed := notification.Completion(old, next); !completed {
		return
	}
	key := desktopUnreadKey(next)
	if next.CompletedAtMs > c.desktopUnread[key] {
		c.desktopUnread[key] = next.CompletedAtMs
		c.persistDesktopUnread()
	}
}

func (c *chatController) markPhoneCompletionRead(r remote.Request) {
	if r.ReadCompletedAtMs <= 0 {
		return
	}
	target := c.phoneThreadFor(r)
	if !target.found || target.thread.CompletedAtMs != r.ReadCompletedAtMs {
		return
	}
	c.markDesktopRead(desktopRead{Key: desktopUnreadKey(target.thread), Version: r.ReadCompletedAtMs})
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
		invalidate()
	}
}

// Selecting a completed conversation explicitly acknowledges it once its
// history arrives. Results already open also become read when displayed below.
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
	app.markDisplayedThreadRead()
}

// Called before building the next frame, so the group cache and list position
// describe what the previous frame actually displayed, not an incoming result
// whose final layout (or scroll-to-end animation) has not happened yet.
func (app *chatApp) markDisplayedThreadRead() {
	if app.mobile || app.section != "" || app.projectDialog || app.switcher.open || app.shortcutsOpen || app.commandPalette.open {
		return
	}
	t := app.c.current()
	if t.Busy || t.Loading || !t.HistoryLoaded || !app.c.desktopThreadUnread(t) {
		return
	}
	g := &app.groups
	if g.thread != t.ID || g.busy || g.revision != app.c.revision() || g.count != len(t.Messages) || len(g.groups) == 0 {
		return
	}
	if g.completedAt != app.c.desktopUnread[desktopUnreadKey(t)] {
		return // a newer completion has not been displayed yet
	}
	list := app.lists[t.ID]
	if list == nil || !list.AtEnd() {
		return
	}
	if !app.c.mirroredHistory(t.ID) {
		return
	}
	app.c.readDesktopThread(t)
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
	if !app.c.mirroredHistory(t.ID) {
		return
	}
	app.desktopReadIntent = desktopRead{}
	// A new turn may have completed while the chosen history was loading.
	if app.c.desktopUnread[read.Key] == read.Version {
		app.c.readDesktopThread(t)
	}
}
