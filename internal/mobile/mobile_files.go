package mobile

import (
	context "context"
	errors "errors"
	phoneproxy "miya/internal/phoneproxy"

	mygo "github.com/egoist/mygo"
)

func (m *App) openProjectFiles(project string) {
	if project == "" || m.FilesCancel != nil {
		return
	}
	if !m.Connected || m.Conn == nil {
		return
	}
	if !m.State.Files {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: tr("Project files"), Message: tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")})
		return
	}
	proxy, err := phoneproxy.NewPhoneFileProxy(m.Conn, project)
	if err != nil {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: tr("Unable to open project files"), Message: err.Error()})
		return
	}
	m.openFileBrowser(proxy, tr("Unable to open project files"))
}

func (m *App) openFileBrowser(proxy *phoneproxy.PhoneFileProxy, errorTitle string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.filesProxy, m.FilesCancel = proxy, cancel
	gen := m.Generation
	go func() {
		err := mygo.OpenInAppBrowser(ctx, proxy.URL)
		proxy.Close()
		m.onMain(gen, func() {
			m.filesProxy, m.FilesCancel = nil, nil
			m.DesktopApproval = MobileDesktopApproval{}
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: errorTitle, Message: err.Error()})
			}
		})
	}()
}
