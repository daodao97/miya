package desktopui

import (
	appmodel "miya/internal/appmodel"
	filepath "path/filepath"

	ui "github.com/egoist/mygo/ui"
)

func (app *App) browseServerProject(path string) {
	if app.projectBrowseLoading {
		return
	}
	app.projectBrowse, app.projectBrowseLoading, app.ProjectError = true, true, ""
	cl := app.C.State().Remote
	go func() {
		var dirs []appmodel.HostEntry
		err := cl.Call("host", appmodel.HostRequest{Action: "browse", Path: path}, &dirs)
		app.C.Dispatch(func() {
			app.projectBrowseLoading = false
			if err != nil {
				app.ProjectError = err.Error()
				return
			}
			app.projectDirectories = dirs
		})
	}()
}

func (app *App) serverProjectFolders(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Server folders").FontSize(12).Grow(1)
		if ui.Button(c.Key("server-parent"), "Parent folder").Disabled(app.projectBrowseLoading).Clicked() {
			app.ProjectPath = filepath.Dir(app.ProjectPath)
			app.browseServerProject(app.ProjectPath)
		}
	})
	if app.projectBrowseLoading {
		ui.Spinner(c)
		return
	}
	ui.Scroll(c.Key("server-folders")).FillWidth().MaxHeight(180).Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			for _, dir := range app.projectDirectories {
				if ui.Button(c.Key(dir.Path), dir.Name).FillWidth().Clicked() {
					app.ProjectPath = dir.Path
					app.browseServerProject(dir.Path)
				}
			}
		})
	})
}
