package desktopui

import (
	appmodel "miya/internal/appmodel"
	widgets "miya/internal/widgets"

	ui "github.com/egoist/mygo/ui"
)

func (app *App) taskStatus(c *ui.Context, t appmodel.Thread) {
	widgets.TaskStatusBar(c, t, app.C.TaskWaitingApproval(t), widgets.TaskPalette{widgets.Panel, widgets.Line, widgets.Ink, widgets.Muted}, func(id, action string) {
		app.Notice = app.C.TaskAction(t.ID, id, action)
	})
}
