package desktopui

import (
	"github.com/egoist/mygo/ui"

	"miya/internal/widgets"
)

func (app *App) chatView(c *ui.Context) {
	cur := app.C.Current()
	if cur.ID == 0 {
		app.home(c)
	} else {
		app.MessageList(c, cur)
	}
	app.composerArea(c)
}

func (app *App) home(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(100).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Padding(0, 0, 40, 0).Children(func() {
		widgets.Icon(c, "brand-openai", 46, widgets.Ink)
		ui.Text(c, tr("Let’s build")).FontSize(28).FontWeight(600)
		pick := widgets.Quiet(c.Key("pick-project")).Height(36)
		pick.Children(func() {
			ui.Text(c, app.project()).FontSize(26).TextColor(widgets.Muted)
			widgets.Icon(c, "chevron-down", 16, widgets.Muted)
		})
		if pick.Clicked() {
			app.OpenProjectDialog()
		}
	})
}
