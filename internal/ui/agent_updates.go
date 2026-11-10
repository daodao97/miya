package desktopui

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"miya/internal/agent/cliupdate"
	"miya/internal/widgets"
)

func (app *App) agentUpdateStatus(c *ui.Context, info cliupdate.Info) {
	if info.Version != "" {
		label := info.Version
		if info.Previous != "" && info.Previous != info.Version {
			label = info.Previous + " → " + info.Version
		}
		ui.Text(c, label).FontSize(11).TextColor(widgets.Muted)
	}
	switch {
	case info.Checking:
		ui.Text(c, tr("Checking for updates…")).FontSize(11).TextColor(widgets.Muted)
	case info.Error != "":
		ui.Text(c, info.Error).FontSize(11).TextColor(widgets.Danger).Selectable()
	case info.Version != "" && info.Via == "":
		ui.Text(c, tr("Update this CLI using its original installer.")).FontSize(11).TextColor(widgets.Muted)
	case info.Latest != "" && !info.Update:
		ui.Text(c, tr("Up to date")).FontSize(11).TextColor(widgets.Muted)
	}
	if info.Previous != "" && info.Previous != info.Version && info.Error == "" {
		ui.Text(c, tr("New agent processes will use the updated CLI. Existing sessions keep running.")).FontSize(11).TextColor(widgets.Muted)
	}
	if info.Command != "" && (info.Update || info.Error != "") {
		ui.Text(c, fmt.Sprintf(tr("Update command: %s"), info.Command)).FontSize(11).Font(widgets.MonoFont).TextColor(widgets.Muted).Selectable()
	}
}
