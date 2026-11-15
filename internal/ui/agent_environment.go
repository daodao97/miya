package desktopui

import (
	"github.com/egoist/mygo/ui"
	"miya/internal/widgets"
)

func (app *App) agentEnvironmentSettings(c *ui.Context) {
	env := app.C.AgentEnvironment()
	if env.Platform != "windows" {
		return
	}
	widgets.SettingsGroup(c, tr("Agent environment"), tr("Agents are installed and run in the selected environment. Windows and WSL keep separate sign-ins."), func() {
		on := env.WSL != ""
		if ui.Switch(c.Key("wsl"), &on).Label(tr("Run agents in WSL")).Disabled(env.Busy).Changed() {
			next := ""
			if on {
				next = "*"
			}
			app.C.SetAgentEnvironment(next)
		}
		if on {
			value := env.WSL
			if value == "*" {
				value = ""
			}
			if draft, ok := app.settingDrafts["agentWSL"]; ok {
				value = draft
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				field := ui.TextInput(c.Key("distribution"), &value).Label(tr("WSL distribution")).Placeholder(tr("Default distribution")).Grow(1).Disabled(env.Busy)
				if field.Changed() {
					app.settingDrafts["agentWSL"] = value
				}
				if widgets.SmallButton(c.Key("save"), tr("Save"), env.Busy).Clicked() {
					next := value
					if next == "" {
						next = "*"
					}
					app.C.SetAgentEnvironment(next)
					delete(app.settingDrafts, "agentWSL")
				}
			})
			ui.Text(c, tr("Use a distribution already installed in WSL, such as Ubuntu.")).FontSize(11).TextColor(widgets.Muted)
		}
		if env.Busy {
			ui.Text(c, tr("Finish running tasks before changing the agent environment.")).FontSize(11).TextColor(widgets.Muted)
		}
	})
}
