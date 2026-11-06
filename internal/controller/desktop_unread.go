package controller

import (
	json "encoding/json"
	appdir "miya/internal/appdir"
	appmodel "miya/internal/appmodel"
	notification "miya/internal/notification"
	remote "miya/internal/remote"
	uiruntime "miya/internal/uiruntime"
	os "os"
	filepath "path/filepath"
)

func (c *Controller) LoadDesktopUnread() {
	c.DesktopUnread = map[string]int64{}
	data, err := os.ReadFile(filepath.Join(appdir.Dir(), "desktop-unread.json"))
	if err == nil {
		_ = json.Unmarshal(data, &c.DesktopUnread)
	}
	if c.DesktopUnread == nil {
		c.DesktopUnread = map[string]int64{}
	}
}

func (c *Controller) persistDesktopUnread() {
	data, err := json.Marshal(c.DesktopUnread)
	if err != nil {
		return
	}
	path := filepath.Join(appdir.Dir(), "desktop-unread.json")
	if os.MkdirAll(filepath.Dir(path), 0700) == nil && os.WriteFile(path+".tmp", data, 0600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func (c *Controller) RecordDesktopCompletion(old, next appmodel.Thread) {
	if c.Mirror != nil || c.DesktopUnread == nil || old.Loading {
		return
	}
	if _, completed := notification.Completion(old, next); !completed {
		return
	}
	key := appmodel.DesktopUnreadKey(next)
	if next.CompletedAtMs > c.DesktopUnread[key] {
		c.DesktopUnread[key] = next.CompletedAtMs
		c.persistDesktopUnread()
	}
}

func (c *Controller) MarkPhoneCompletionRead(r remote.Request) {
	if r.ReadCompletedAtMs <= 0 {
		return
	}
	target := c.PhoneThreadFor(r)
	if !target.Found || target.Thread.CompletedAtMs != r.ReadCompletedAtMs {
		return
	}
	c.MarkDesktopRead(appmodel.DesktopRead{Key: appmodel.DesktopUnreadKey(target.Thread), Version: r.ReadCompletedAtMs})
}

func (c *Controller) DesktopThreadUnread(t appmodel.Thread) bool {
	key := appmodel.DesktopUnreadKey(t)
	version := c.DesktopUnread[key]
	return version > 0 && c.DesktopReadPending[key] < version
}

func (c *Controller) ReadDesktopThread(t appmodel.Thread) {
	if !c.DesktopThreadUnread(t) || t.Busy || t.Loading {
		return
	}
	key := appmodel.DesktopUnreadKey(t)
	read := appmodel.DesktopRead{Key: key, Version: c.DesktopUnread[key]}
	if c.Remote != nil {
		if c.DesktopReadPending == nil {
			c.DesktopReadPending = map[string]int64{}
		}
		c.DesktopReadPending[key] = read.Version
		c.forward("desktop-read", read)
		return
	}
	c.MarkDesktopRead(read)
}

func (c *Controller) MarkDesktopRead(read appmodel.DesktopRead) {
	// A delayed acknowledgement must not clear a newer completed turn.
	if version := c.DesktopUnread[read.Key]; version > 0 && read.Version >= version {
		delete(c.DesktopUnread, read.Key)
		c.persistDesktopUnread()
		uiruntime.Invalidate()
	}
}
