package desktopui

import (
	"strings"

	"miya/internal/appmodel"
	"miya/internal/composer"
	"miya/internal/model"
)

// commandChoices lists the commands matching query for the open
// conversation's agent: Claude Code's own after Miya's in its conversations.
func (app *App) CommandChoices(query string) []model.ComposerChoice {
	if app.C.CurrentAgent() == appmodel.AgentClaude {
		app.C.ClaudeCommandsFor(app.C.State().Cwd)
	}
	return app.knownCommandChoices(query)
}

// Styling an existing draft uses cached metadata; it must not launch a CLI.
func (app *App) knownCommandChoices(query string) []model.ComposerChoice {
	c := app.C
	if c.CurrentAgent() != appmodel.AgentClaude {
		return composer.CommandChoices(query)
	}
	q := strings.ToLower(query)
	seen := map[string]bool{}
	var out []model.ComposerChoice
	for _, cmd := range composer.ComposerCommands {
		if appmodel.AppUICommands[cmd.Name] && strings.Contains(cmd.Name, q) {
			out, seen[cmd.Name] = append(out, cmd), true
		}
	}
	list := c.KnownClaudeCommands(c.State().Cwd)
	for _, cmd := range list {
		if !seen[cmd.Name] && strings.Contains(strings.ToLower(cmd.Name), q) {
			out, seen[cmd.Name] = append(out, cmd), true
		}
	}
	return out
}

// refreshCommandChoices updates an open command menu once Claude Code's
// commands arrive.
func (app *App) refreshCommandChoices() {
	m := &app.Complete
	if m.Open && m.Token.Kind == '/' {
		m.Choices = app.CommandChoices(m.Token.Query)
		m.state = app.commandMenuState()
	}
}

// commandMenuState is what the command menu says when it lists nothing.
func (app *App) commandMenuState() string {
	c := app.C
	if c.CurrentAgent() == appmodel.AgentClaude {
		if _, known := c.ClaudeCommandsFor(c.State().Cwd); !known {
			return "Loading Claude Code commands…"
		}
	}
	return "No matching commands"
}
