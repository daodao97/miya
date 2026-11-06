package desktopui

import (
	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/widgets"
)

func (app *App) taskStatus(c *ui.Context, t model.Thread) {
	widgets.TaskStatusBar(c, t, app.C.TaskWaitingApproval(t), widgets.TaskPalette{Surface: widgets.Panel, Border: widgets.Line, Ink: widgets.Ink, Muted: widgets.Muted}, func(id, action string) {
		app.Notice = app.C.TaskAction(t.ID, id, action)
	})
}
