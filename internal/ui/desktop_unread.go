package desktopui

import (
	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/uiruntime"
)

// Selecting a completed conversation explicitly acknowledges it once its
// history arrives. Results already open also become read when displayed below.
func (app *App) RequestDesktopRead(t model.Thread) {
	app.desktopReadIntent = appmodel.DesktopRead{}
	if !app.C.DesktopThreadUnread(t) || t.Busy && !t.Loading {
		return
	}
	key := appmodel.DesktopUnreadKey(t)
	app.desktopReadIntent = appmodel.DesktopRead{Key: key, Version: app.C.State().DesktopUnread[key]}
}

func (app *App) MarkVisibleThreadRead() {
	w := uiruntime.CurrentWindow()
	if w == nil || !w.IsFocused() || w.IsMinimized() {
		return
	}
	app.MarkRequestedThreadRead()
	app.MarkDisplayedThreadRead()
}

// Called before building the next frame, so the group cache and list position
// describe what the previous frame actually displayed, not an incoming result
// whose final layout (or scroll-to-end animation) has not happened yet.
func (app *App) MarkDisplayedThreadRead() {
	if app.Mobile || app.Section != "" || app.ProjectDialog || app.Switcher.Open || app.ShortcutsOpen || app.CommandPalette.Open {
		return
	}
	t := app.C.Current()
	if t.Busy || t.Loading || !t.HistoryLoaded || !app.C.DesktopThreadUnread(t) {
		return
	}
	g := &app.Groups
	if g.Thread != t.ID || g.Busy || g.Revision != app.C.State().Revision() || g.Count != len(t.Messages) || len(g.Groups) == 0 {
		return
	}
	if g.CompletedAt != app.C.State().DesktopUnread[appmodel.DesktopUnreadKey(t)] {
		return // a newer completion has not been displayed yet
	}
	list := app.Lists[t.ID]
	if list == nil || !list.AtEnd() {
		return
	}
	if !app.C.MirroredHistory(t.ID) {
		return
	}
	app.C.ReadDesktopThread(t)
}

func (app *App) MarkRequestedThreadRead() {
	if app.Section != "" || app.desktopReadIntent.Key == "" {
		return
	}
	t := app.C.Current()
	read := app.desktopReadIntent
	if appmodel.DesktopUnreadKey(t) != read.Key || t.Busy || t.Loading || !t.HistoryLoaded {
		return
	}
	if !app.C.MirroredHistory(t.ID) {
		return
	}
	app.desktopReadIntent = appmodel.DesktopRead{}
	// A new turn may have completed while the chosen history was loading.
	if app.C.State().DesktopUnread[read.Key] == read.Version {
		app.C.ReadDesktopThread(t)
	}
}
