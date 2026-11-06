package widgets

import "miya/internal/interaction"

func RequestAgent(a interaction.Request) string { return a.AgentName() }
