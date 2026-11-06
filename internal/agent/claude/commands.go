package claude

import (
	"miya/internal/agent"
	"miya/internal/composer"
	"miya/internal/model"
)

func (b Adapter) Commands(project string, load bool) agent.CommandCatalog {
	c := b.host
	choices, known := c.Commands(project, load)
	var host []model.ComposerChoice
	for _, cmd := range composer.ComposerCommands {
		if agent.IsHostCommand(cmd.Name) {
			host = append(host, cmd)
		}
	}
	return agent.CommandCatalog{Choices: agent.MergeCommands(host, choices), PhoneChoices: choices, Known: known, Native: true, LoadingMessage: tr("Loading Claude Code commands…")}
}
