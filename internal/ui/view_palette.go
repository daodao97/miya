package desktopui

import (
	appmodel "miya/internal/appmodel"
	uiruntime "miya/internal/uiruntime"
	widgets "miya/internal/widgets"

	mygo "github.com/egoist/mygo"
	ui "github.com/egoist/mygo/ui"
	markdown "github.com/egoist/mygo/ui/markdown"
)

func (app *App) wantsDark() bool {
	switch app.Appearance {
	case "dark":
		return true
	case "light":
		return false
	}
	return widgets.SystemDark.Load()
}

// applyAppearance switches the palette when the wanted appearance changes.
func (app *App) ApplyAppearance() {
	if dark := app.wantsDark(); !app.themed || dark != app.Dark {
		app.Theme = widgets.ApplyDesktopPalette(dark)
		app.Md.Theme = &markdown.Theme{Link: widgets.LinkInk}
		app.Dark, app.themed = dark, true
	}
}

func (app *App) setAppearance(v string) {
	v = appmodel.NormalizeAppearance(v)
	app.Appearance = v
	appmodel.PersistSetting("appearance", v)
	if uiruntime.CurrentWindow() != nil {
		mygo.Theme.SetSource(widgets.ThemeSource(v))
		widgets.SystemDark.Store(mygo.Theme.IsDark())
	}
	uiruntime.Invalidate()
}

// windowBackground fills a new window until its first frame paints.
func windowBackground(app *App) string {
	if app.wantsDark() {
		return "#1a1a1c"
	}
	return "#ffffff"
}

// appearanceSettings chooses light, dark or the system's appearance.
func (app *App) appearanceSettings(c *ui.Context) {
	title := tr("Appearance")
	widgets.SettingRow(c, title, "", func() {
		ids := []string{"", "light", "dark"}
		i := 0
		for j, id := range ids {
			if app.Appearance == id {
				i = j
			}
		}
		before := i
		ui.Segmented(c.Key("appearance"), &i,
			tr("System"), tr("Light"), tr("Dark")).Label(title)
		if i != before {
			app.setAppearance(ids[i])
		}
	})
}
