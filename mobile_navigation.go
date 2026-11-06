package main

import (
	"miya/internal/model"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Primary destinations belong to the drawer; only their child pages pop
// the navigation stack. Rendering, buttons and edge gestures share this rule.
func mobilePageHasDrawer(path string) bool {
	switch path {
	case "/", "/thread", "/automations", "/memory", "/settings", "/desktops":
		return true
	}
	return false
}

func (m *mobileApp) openDrawerPage(path string) {
	m.menu = false
	m.drawerSearch, m.drawerQuery = false, ""
	m.router().Push(path)
}

func (m *mobileApp) navigationBack(c *ui.Context, path string) {
	label, glyph := tr("Back"), "arrow-left"
	if mobilePageHasDrawer(path) {
		label, glyph = tr("Open sidebar"), "layout-sidebar"
	}
	if m.circle(c, "back", label, func() { icon(c, glyph, 20, m.pal.ink) }).Expanded(m.drawerVisible).Clicked() {
		if mobilePageHasDrawer(path) {
			m.menu = true
		} else {
			m.router().Back()
		}
	}
}

// Conversation links stay inside the connected desktop's namespace on phones.
func (m *mobileApp) openMessageLink(link string) {
	if _, ok := model.ParseThreadURL(link); ok {
		m.openCollaborationThread(link)
		return
	}
	if ui.SafeToOpen(link) {
		go mygo.Shell.OpenExternal(link)
	}
}
