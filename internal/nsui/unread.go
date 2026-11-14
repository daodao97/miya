package nsui

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/uiruntime"
)

// readTracker marks completed conversations read once the user has seen
// them, as MyGo's desktop does: one chosen explicitly is read once its
// history arrives; one already open is read when its end shows in a
// focused window.
type readTracker struct {
	c      Host
	intent appmodel.DesktopRead
}

// request remembers that the user chose t, so its completion as of now
// counts as seen once the history shows.
func (r *readTracker) request(t model.Thread) {
	r.intent = appmodel.DesktopRead{}
	if !r.c.DesktopThreadUnread(t) || t.Busy && !t.Loading {
		return
	}
	key := appmodel.DesktopUnreadKey(t)
	r.intent = appmodel.DesktopRead{Key: key, Version: r.c.State().DesktopUnread[key]}
}

// markVisible reads the current conversation when the user can see it:
// the conversation shows, the window has the focus, and its history is
// loaded and mirrored.
func (r *readTracker) markVisible(showing, atLatest bool) {
	if !showing {
		return
	}
	if w := uiruntime.CurrentWindow(); w == nil || !w.IsFocused() || w.IsMinimized() {
		return
	}
	t := r.c.Current()
	if t.ID == 0 || t.Busy || t.Loading || !t.HistoryLoaded || !r.c.MirroredHistory(t.ID) {
		return
	}
	if read := r.intent; read.Key != "" && appmodel.DesktopUnreadKey(t) == read.Key {
		r.intent = appmodel.DesktopRead{}
		// A newer turn may have completed while the history loaded.
		if r.c.State().DesktopUnread[read.Key] == read.Version {
			r.c.ReadDesktopThread(t)
			return
		}
	}
	if atLatest && len(t.Messages) > 0 && r.c.DesktopThreadUnread(t) {
		r.c.ReadDesktopThread(t)
	}
}
