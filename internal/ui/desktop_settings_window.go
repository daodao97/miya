package desktopui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// settingsWindowOpener opens Settings in a window of its own. The desktop
// app installs it; without it (tests, no window system) Settings shows in
// place of the chat.
var settingsWindowOpener func(*App)

// settingsWindow is the open Settings window, nil when closed.
var settingsWindow *mygo.Window

// openSettings shows Settings, on tab when tab is not negative. It reports
// whether Settings opened in its own window.
func (app *App) openSettings(tab int) bool {
	app.closeMenus()
	if tab >= 0 {
		app.SettingsTab = tab
	}
	if app.SettingsTab == remoteControlSettingsTab {
		app.refreshRemotePermissions()
	}
	if settingsWindowOpener != nil && !app.Mobile {
		if app.Section == "Settings" {
			app.Section = ""
		}
		settingsWindowOpener(app)
		return true
	}
	app.Section = "Settings"
	return false
}

// showSettingsWindow opens the Settings window, or brings it forward.
func (app *App) showSettingsWindow() {
	if w := settingsWindow; w != nil && !w.IsDestroyed() {
		w.Show()
		w.Focus()
		w.Invalidate()
		mygo.App.Focus()
		return
	}
	hub := app.EnsureDesktopWorkspaces()
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:                tr("Settings"),
		Width:                780,
		Height:               700,
		MinWidth:             560,
		MinHeight:            420,
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 20},
		BackgroundColor:      windowBackground(hub.Active),
		StateKey:             "settings",
		Content:              ui.View(hub.settingsWindowView),
	})
	settingsWindow = w
	remove := uiruntime.AddCompanionWindow(w)
	w.OnFocus(func() {
		w.Update(func() {
			if active := hub.Active; active.SettingsTab == remoteControlSettingsTab {
				active.refreshRemotePermissions()
			}
		})
	})
	w.OnClosed(func() {
		remove()
		if settingsWindow == w {
			settingsWindow = nil
		}
	})
	w.Focus()
	mygo.App.Focus()
}

// settingsWindowView draws the active workspace's Settings under a title
// bar that drags the window.
func (m *DesktopWorkspaces) settingsWindowView(ctx *ui.Context) {
	app := m.Active
	widgets.UseInterfaceLanguage(ctx)
	app.ApplyAppearance()
	ctx.SetTheme(app.Theme)
	ctx = ctx.Key("workspace:" + m.Store.Active)
	ui.Column(ctx).Fill().Background(widgets.Canvas).TextColor(widgets.Ink).FontSize(13).Children(func() {
		ui.Row(ctx).FillWidth().Height(52).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).DragWindow().Children(func() {
			ui.Text(ctx, tr("Settings")).FontSize(13).FontWeight(600).SingleLine()
		})
		ui.Scroll(ctx.Key("settings-window")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(ctx).Padding(4, 28, 28, 28).Children(func() {
				app.settingsView(ctx)
			})
		})
	})
	// What Settings changes (appearance, language, agents…) shows in the
	// main window too. The main window never redraws this one from its own
	// frames, so the two cannot keep each other drawing.
	if w := mainWindow(); w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
}
