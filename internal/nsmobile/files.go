package nsmobile

import (
	"net/url"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	phoneproxy "miya/internal/phone/proxy"
	webservice "miya/internal/runtime/web"
)

// showActions is the conversation actions menu as the system's action sheet
// (UIAlertController's on iOS), as the old phone app's: the actions
// available now, then Cancel.
func (m *App) showActions() {
	type item struct {
		label string
		on    func()
	}
	var items []item
	add := func(ok bool, label string, on func()) {
		if ok {
			items = append(items, item{label, on})
		}
	}
	add(true, tr("Automations"), func() { m.pages.Open("automations") })
	add(true, tr("Shared memory"), func() { m.pages.Open("memory") })
	add(m.Open != 0 && m.State.Collaboration != nil && m.State.Collaboration.Version >= 1, tr("Agent tasks"), func() { m.pages.Open("delegations") })
	add(m.Connected && m.Open != 0 && m.Viewer.Current().ServerID != "", tr("Web services"), m.openWebServices)
	add(m.Connected && m.ComposerProject() != "", tr("Project files"), func() { m.pages.Open(""); m.openProjectFile(m.ComposerProject(), "") })
	add(true, tr("New conversation"), func() {
		project, agent := m.ComposerProject(), m.Agent()
		m.StartConversation(project, agent)
		m.Path = "/thread"
		m.pages.Open("")
	})
	add(m.Open != 0 && !m.Sending, tr("Hand off to a new conversation"), func() { m.Handoff(); m.Path = "/thread"; m.pages.Open("") })
	add(m.Connected && m.State.DesktopControl && m.DesktopRequest == "", tr("Open remote desktop"), func() { m.closeFileBrowser(); m.pages.Open(""); m.OpenRemoteDesktop() })
	actions := make([]core.AlertAction, 0, len(items)+1)
	for _, it := range items {
		actions = append(actions, core.AlertAction{Label: it.label})
	}
	actions = append(actions, core.AlertAction{Label: tr("Cancel"), Role: core.AlertCancel})
	m.pages.menu.Set(true)
	base.ShowAlertDialog(m.cx, base.AlertDialog{Title: tr("Conversation actions"), Presentation: base.AlertPresentActionSheet, Actions: actions}, func(label string) {
		m.pages.menu.Set(false)
		for _, it := range items {
			if it.label == label {
				m.act(it.on)()
				return
			}
		}
		m.change()
	})
}
func (m *App) openMessageLink(raw string) {
	m.act(func() {
		if _, ok := model.ParseThreadURL(raw); ok {
			m.OpenReference(raw)
			return
		}
		if relative, ok := msgfmt.LocalFileLink(m.ComposerProject(), raw); ok {
			project, file, found := m.FileTarget(relative)
			if !found {
				m.SendErr = tr("File is outside the project directory")
				return
			}
			m.openProjectFile(project, file)
			return
		}
		if port, path, ok := webservice.LocalLink(raw); ok {
			m.openLocalWebLink(port, path)
			return
		}
		if core.CheckURL(raw) == nil {
			m.cx.OpenURL(raw, nil)
		}
	})()
}
func (m *App) openProjectFile(project, file string) {
	if m.Conn == nil || !m.Connected {
		m.SendErr = tr("Not connected to desktop")
		return
	}
	if !m.State.Files {
		m.SendErr = tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")
		return
	}
	m.closeWebServices()
	m.closeFileBrowser()
	proxy, err := phoneproxy.NewPhoneFileProxy(m.Conn, project)
	if err != nil {
		m.SendErr = tr("Unable to open project files") + ": " + err.Error()
		return
	}
	if file != "" {
		proxy.URL += "?path=" + url.QueryEscape((&url.URL{Path: "/" + file}).EscapedPath())
	}
	// The proxy's authenticated loopback URL stays inside an app WebView and
	// is closed on Back, background, disconnect or a replacement connection.
	m.closeFiles = func() { proxy.Close() }
	m.filesURL.Set(proxy.URL)
}
func (m *App) closeFileBrowser() {
	m.webBrowser = false
	if m.closeFiles != nil {
		m.closeFiles()
		m.closeFiles = nil
	}
	if m.filesURL != nil {
		m.filesURL.Set("")
	}
}
func (m *App) fileView(cx *core.Scope) core.Node {
	conn, owner := m.Conn, m.composerOwner()
	label, id, failure := tr("Project files"), "project-files-webview", tr("Unable to open project files")
	if m.webBrowser {
		label, id, failure = tr("Web services"), "web-service-webview", tr("Unable to open web service")
	}
	cx.Effect(func() {
		m.revision.Get()
		if m.Conn != conn || !m.Connected || m.composerOwner() != owner {
			cx.Post(m.closeFileBrowser)
		}
	})
	return core.View{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		m.button("close-files", tr("Back"), nil, m.closeFileBrowser),
		core.WebView{Source: core.WebSrc(core.Untracked(cx, m.filesURL.Get)), Label: label, TestID: id, Style: core.Style{FlexGrow: 1},
			OnError: func(err error) {
				m.SendErr = failure + ": " + err.Error()
				m.closeFileBrowser()
				m.change()
			}},
	}}
}
