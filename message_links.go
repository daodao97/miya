package main

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/model"
)

// Copy file destinations rather than their displayed label or URL encoding.
// On phones cwd is the connected desktop's project, never a phone-local path.
func messageLinkMenu(c *ui.Context, cwd string) func(*ui.Menu, string) {
	services := c.Services()
	return func(m *ui.Menu, link string) {
		label, text := tr("Copy link"), link
		if rel, ok := localFileLink(cwd, link); ok {
			label, text = tr("Copy path"), filepath.Clean(filepath.Join(cwd, rel))
		}
		if m.Item(label).Chosen() {
			services.WriteClipboard(text)
		}
		m.Separator()
		m.EditItems()
	}
}

// Message file links use the conversation's workspace, just as the agent does.
// Reading them in the files surface also preserves any unsaved editor content.
func (app *chatApp) openMessageLink(link string) {
	if _, ok := model.ParseThreadURL(link); ok {
		app.openCollaborationThread(link)
		return
	}
	if rel, ok := localFileLink(app.c.cwd, link); ok {
		p := app.panel()
		app.openSurface(p, surfaceFiles)
		app.openFile(p, rel)
		return
	}
	if ui.SafeToOpen(link) {
		go mygo.Shell.OpenExternal(link)
	}
}
