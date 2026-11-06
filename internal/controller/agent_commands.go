package controller

import (
	"miya/internal/agent"
	"miya/internal/composer"
	"miya/internal/model"
)

func (b CodexBackend) Commands(_ string, _ bool) agent.CommandCatalog {
	return agent.DefaultCommands(b.ID())
}
func (b claudeBackend) Commands(project string, load bool) agent.CommandCatalog {
	c := b.C
	list, known := c.ClaudeCmdCache[project]
	choices := list.Choice
	if load {
		choices, known = c.ClaudeCommandsFor(project)
	}
	var host []model.ComposerChoice
	for _, cmd := range composer.ComposerCommands {
		if agent.HostCommands[cmd.Name] {
			host = append(host, cmd)
		}
	}
	return agent.CommandCatalog{Choices: agent.MergeCommands(host, choices), PhoneChoices: choices, Known: known, Native: true, LoadingMessage: tr("Loading Claude Code commands…")}
}
