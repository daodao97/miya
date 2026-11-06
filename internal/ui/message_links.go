package desktopui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/model"
	"miya/internal/msgfmt"
)

// Message file links use the conversation's workspace, just as the agent does.
// Reading them in the files surface also preserves any unsaved editor content.
func (app *App) OpenMessageLink(link string) {
	if _, ok := model.ParseThreadURL(link); ok {
		app.openCollaborationThread(link)
		return
	}
	if rel, ok := msgfmt.LocalFileLink(app.C.State().Cwd, link); ok {
		p := app.Panel()
		app.openSurface(p, SurfaceFiles)
		app.openFile(p, rel)
		return
	}
	if ui.SafeToOpen(link) {
		go mygo.Shell.OpenExternal(link)
	}
}
