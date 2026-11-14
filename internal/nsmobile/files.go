package nsmobile

import (
	"net/url"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	phoneproxy "miya/internal/phone/proxy"
)

func (m *App) conversationMenu(cx *core.Scope) core.Node {
	return scroll(
		m.button("menu-automations", tr("Automations"), nil, func() { m.sheet.Set("automations") }),
		m.button("menu-memory", tr("Shared memory"), nil, func() { m.sheet.Set("memory") }),
		m.button("menu-delegations", tr("Agent tasks"), live(m, func() bool { return m.Open == 0 || m.State.Collaboration == nil || m.State.Collaboration.Version < 1 }), func() { m.sheet.Set("delegations") }), text(tr("Conversation actions")),
		m.button("web-services", tr("Web services"), live(m, func() bool { return !m.Connected || m.Open == 0 || m.Viewer.Current().ServerID == "" }), m.openWebServices),
		m.button("project-files", tr("Project files"), live(m, func() bool { return !m.Connected || m.ComposerProject() == "" }), func() { m.sheet.Set(""); m.openProjectFile(m.ComposerProject(), "") }),
		m.button("new-from-menu", tr("New conversation"), nil, func() {
			project, agent := m.ComposerProject(), m.Agent()
			m.StartConversation(project, agent)
			m.Path = "/thread"
			m.sheet.Set("")
		}),
		m.button("handoff", tr("Hand off to a new conversation"), live(m, func() bool { return m.Open == 0 || m.Sending }), func() { m.Handoff(); m.Path = "/thread"; m.sheet.Set("") }),
		// TODO(remote-desktop): add their sheet actions when their
		// respective NSGo adapters are implemented. No inactive controls are shown.
		m.button("actions-cancel", tr("Cancel"), nil, func() { m.sheet.Set("") }),
	)
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
