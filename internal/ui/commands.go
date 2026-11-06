package desktopui

import "miya/internal/model"

func (app *App) CommandChoices(query string) []model.ComposerChoice {
	return app.C.CurrentAgentControls().Commands(app.C.State().Cwd, true).Filter(query)
}

// Styling a draft reads cached metadata and must not launch a native process.
func (app *App) knownCommandChoices(query string) []model.ComposerChoice {
	return app.C.CurrentAgentControls().Commands(app.C.State().Cwd, false).Filter(query)
}
func (app *App) refreshCommandChoices() {
	m := &app.Complete
	if m.Open && m.Token.Kind == '/' {
		m.Choices = app.CommandChoices(m.Token.Query)
		m.state = app.commandMenuState()
	}
}
func (app *App) commandMenuState() string {
	catalog := app.C.CurrentAgentControls().Commands(app.C.State().Cwd, true)
	if !catalog.Known {
		return catalog.LoadingMessage
	}
	return "No matching commands"
}
