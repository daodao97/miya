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
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: "项目文件", Message: "请先更新桌面端 Miya。后台有任务时，会在空闲后自动更新。"})
		return
	}
	proxy, err := newPhoneFileProxy(m.conn, project)
	if err != nil {
		go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: "无法打开项目文件", Message: err.Error()})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.filesProxy, m.filesCancel = proxy, cancel
	gen := m.generation
	go func() {
		err := mygo.OpenInAppBrowser(ctx, proxy.URL)
		proxy.Close()
		m.onMain(gen, func() {
			m.filesProxy, m.filesCancel = nil, nil
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: "无法打开项目文件", Message: err.Error()})
			}
		})
	}()
}
