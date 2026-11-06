package mobile

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/widgets"
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

func (m *App) OpenDrawerPage(path string) {
	m.Menu = false
	m.DrawerSearch, m.DrawerQuery = false, ""
	m.Router().Push(path)
}

func (m *App) navigationBack(c *ui.Context, path string) {
	label, glyph := tr("Back"), "arrow-left"
	if mobilePageHasDrawer(path) {
		label, glyph = tr("Open sidebar"), "layout-sidebar"
	}
	if m.circle(c, "back", label, func() { widgets.Icon(c, glyph, 20, m.Pal.ink) }).Expanded(m.DrawerVisible).Clicked() {
		if mobilePageHasDrawer(path) {
			m.Menu = true
		} else {
			m.Router().Back()
		}
	}
}

// Conversation links stay inside the connected desktop's namespace on phones.
func (m *App) openMessageLink(link string) {
	if _, ok := model.ParseThreadURL(link); ok {
		m.openCollaborationThread(link)
		return
	}
	if ui.SafeToOpen(link) {
		go mygo.Shell.OpenExternal(link)
	}
}
