package nsmobile

import (
	"net/url"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/chat/model"
	phoneproxy "miya/internal/phone/proxy"
	webservice "miya/internal/runtime/web"
)

func (m *App) openMessageLink(raw string) {
	m.act(func() {
		if _, ok := model.ParseThreadURL(raw); ok {
			m.OpenReference(raw)
			return
		}
		if project, file, ok := m.MessageFileTarget(raw); ok {
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
	single := project == "" && file != ""
	if (single && !m.State.RemoteFiles) || (!single && !m.State.Files) {
		m.SendErr = tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")
		return
	}
	m.closeWebServices()
	m.closeFileBrowser()
	var proxy *phoneproxy.PhoneFileProxy
	var err error
	if single {
		proxy, err = phoneproxy.NewRemoteFileProxy(m.Conn, file)
	} else {
		proxy, err = phoneproxy.NewPhoneFileProxy(m.Conn, project)
	}
	if err != nil {
		m.SendErr = tr("Unable to open project files") + ": " + err.Error()
		return
	}
	if !single && file != "" {
		proxy.URL += "?path=" + url.QueryEscape((&url.URL{Path: "/" + file}).EscapedPath())
	}
	// The proxy's authenticated loopback URL stays inside an app WebView and
	// is closed on Back, background, disconnect or a replacement connection.
	m.closeFiles = proxy.Close
	m.singleFile = single
	m.filesURL.Set(proxy.URL)
}
func (m *App) closeFileBrowser() {
	m.webBrowser, m.singleFile = false, false
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
	if m.singleFile {
		label, id = tr("Files"), "remote-file-webview"
	}
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
		m.navigationBar(cx, label, "close-files", m.closeFileBrowser),
		core.WebView{Source: core.WebSrc(core.Untracked(cx, m.filesURL.Get)), Label: label, TestID: id, NoJavaScript: m.singleFile, Style: core.Style{FlexGrow: 1},
			OnError: func(err error) {
				m.SendErr = failure + ": " + err.Error()
				m.closeFileBrowser()
				m.change()
			}},
	}}
}
