package main

import (
	"context"
	"errors"

	"github.com/egoist/mygo"
)

func (m *mobileApp) openProjectFiles(project string) {
	if project == "" || m.filesCancel != nil {
		return
	}
	if !m.connected || m.conn == nil {
		return
	}
	if !m.state.Files {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: tr("Project files"), Message: tr("Update Miya on your desktop first. If the background service is busy, it will update when idle.")})
		return
	}
	proxy, err := newPhoneFileProxy(m.conn, project)
	if err != nil {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: tr("Unable to open project files"), Message: err.Error()})
		return
	}
	m.openFileBrowser(proxy, tr("Unable to open project files"))
}

func (m *mobileApp) openFileBrowser(proxy *phoneFileProxy, errorTitle string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.filesProxy, m.filesCancel = proxy, cancel
	gen := m.generation
	go func() {
		err := mygo.OpenInAppBrowser(ctx, proxy.URL)
		proxy.Close()
		m.onMain(gen, func() {
			m.filesProxy, m.filesCancel = nil, nil
			m.desktopApproval = mobileDesktopApproval{}
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: errorTitle, Message: err.Error()})
			}
		})
	}()
}
