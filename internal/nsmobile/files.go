package nsmobile

import (
	"net/url"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	phoneproxy "miya/internal/phone/proxy"

	"github.com/nsgo-dev/nsgo/core"
)

func (m *App) conversationMenu(cx *core.Scope) core.Node {
	return scroll(text(tr("Conversation actions")),
		m.button("project-files", tr("Project files"), live(m, func() bool { return !m.Connected || m.ComposerProject() == "" }), func() { m.sheet.Set(""); m.openProjectFile(m.ComposerProject(), "") }),
		m.button("new-from-menu", tr("New conversation"), nil, func() {
			project, agent := m.ComposerProject(), m.Agent()
			m.StartConversation(project, agent)
			m.Path = "/thread"
			m.sheet.Set("")
		}),
		m.button("handoff", tr("Hand off to a new conversation"), live(m, func() bool { return m.Open == 0 || m.Sending }), func() { m.Handoff(); m.Path = "/thread"; m.sheet.Set("") }),
		m.button("remote-desktop", tr("Open remote desktop"), live(m, func() bool { return !m.Connected || !m.State.DesktopControl || m.DesktopRequest != "" }), func() { m.closeFileBrowser(); m.sheet.Set(""); m.OpenRemoteDesktop() }),
		m.errorText(cx),
		// TODO(web-services): add its owned destination and proxy.
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
	if m.closeFiles != nil {
		m.closeFiles()
		m.closeFiles = nil
	}
	if m.filesURL != nil {
		m.filesURL.Set("")
	}
}
func (m *App) fileView(cx *core.Scope) core.Node {
	conn := m.Conn
	cx.Effect(func() {
		m.revision.Get()
		if m.Conn != conn || !m.Connected {
			cx.Post(m.closeFileBrowser)
		}
	})
	return core.View{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		m.button("close-files", tr("Back"), nil, m.closeFileBrowser),
		core.WebView{Source: core.WebSrc(core.Untracked(cx, m.filesURL.Get)), Label: tr("Project files"), TestID: "project-files-webview", Style: core.Style{FlexGrow: 1},
			OnError: func(err error) {
				m.SendErr = tr("Unable to open project files") + ": " + err.Error()
				m.closeFileBrowser()
				m.change()
			}},
	}}
}
