package mobile

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/browser"

	phoneproxy "miya/internal/phone/proxy"
)

func (m *App) openProjectFiles(project string) {
	m.openProjectFile(project, "")
}

// Resolve against the conversation's desktop workspace, then select the most
// specific registered project. Never send a desktop path to the phone's OS.
func (m *App) messageFileTarget(relative string) (project, file string, ok bool) {
	cwd := m.ComposerProject()
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", "", false
	}
	absolute := filepath.Clean(filepath.Join(cwd, relative))
	for _, candidate := range m.State.Projects {
		root := filepath.Clean(candidate.Path)
		if !filepath.IsAbs(root) || filepath.Dir(root) == root && root != filepath.Clean(cwd) {
			continue
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if !ok || len(root) > len(project) {
			project, file, ok = root, filepath.ToSlash(rel), true
		}
	}
	return
}

func (m *App) openProjectFile(project, file string) {
	if project == "" || m.FilesCancel != nil {
		return
	}
	if !m.Connected || m.Conn == nil {
		m.SendErr = tr("Not connected to desktop")
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
	if file != "" {
		// Bootstrap the existing authenticated browser session at this file.
		// URL-encode twice: once for the filename, once for the query value.
		target := (&url.URL{Path: "/" + file}).EscapedPath()
		proxy.URL += "?path=" + url.QueryEscape(target)
	}
	m.openFileBrowser(proxy, tr("Unable to open project files"))
}

func (m *App) openFileBrowser(proxy *phoneproxy.PhoneFileProxy, errorTitle string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.filesProxy, m.FilesCancel = proxy, cancel
	gen := m.Generation
	go func() {
		err := browser.Open(ctx, proxy.URL)
		proxy.Close()
		m.onMain(gen, func() {
			m.filesProxy, m.FilesCancel = nil, nil
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				go mygo.Dialog.Message(mygo.MessageOptions{Parent: m.win, Title: errorTitle, Message: err.Error()})
			}
		})
	}()
}
