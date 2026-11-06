package main

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

func (c *chatController) remoteHost() bool {
	return c != nil && c.remote != nil && (c.remote.Wire() != nil || c.mirror != nil && c.mirror.remoteHost)
}

func (app *chatApp) browseServerProject(path string) {
	if app.projectBrowseLoading {
		return
	}
	app.projectBrowse, app.projectBrowseLoading, app.projectError = true, true, ""
	cl := app.c.remote
	go func() {
		var dirs []hostEntry
		err := cl.Call("host", hostRequest{Action: "browse", Path: path}, &dirs)
		app.c.dispatch(func() {
			app.projectBrowseLoading = false
			if err != nil {
				app.projectError = err.Error()
				return
			}
			app.projectDirectories = dirs
		})
	}()
}

func (app *chatApp) serverProjectFolders(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Server folders").FontSize(12).Grow(1)
		if ui.Button(c.Key("server-parent"), "Parent folder").Disabled(app.projectBrowseLoading).Clicked() {
			app.projectPath = filepath.Dir(app.projectPath)
			app.browseServerProject(app.projectPath)
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
					app.projectPath = dir.Path
					app.browseServerProject(dir.Path)
				}
			}
		})
	})
}
