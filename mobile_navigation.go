package main

import "github.com/egoist/mygo/ui"

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
	label := "返回"
	if mobilePageHasDrawer(path) {
		label = "打开侧边栏"
	}
	if m.circle(c, "back", label, func() { icon(c, "arrow-left", 20, m.pal.ink) }).Clicked() {
		if mobilePageHasDrawer(path) {
			m.menu = true
		} else {
			m.router().Back()
		}
	}
}
