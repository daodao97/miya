package appmodel

import (
	"miya/internal/agent"
	"miya/internal/agentmeta"
)

var AppUICommands = agent.HostCommands

func ClaudeTakesCommand(id, command string) bool {
	return (agent.CommandCatalog{Native: agentmeta.For(id).NativeCommands}).TakesCommand(command)
}
