package integration

import (
	"miya/internal/agent"
	"miya/internal/appmodel"
	"testing"
)

func TestAgentCommandRouting(t *testing.T) {
	for _, tc := range []struct {
		agent, command string
		want           bool
	}{
		{appmodel.AgentClaude, "review the diff", true},
		{appmodel.AgentClaude, "lark-im", true},
		{appmodel.AgentClaude, "compact", true},
		{appmodel.AgentClaude, "model", false},
		{appmodel.AgentClaude, "new", false},
		{appmodel.AgentCodex, "lark-im", false},
		{appmodel.AgentCodex, "compact", false},
	} {
		if got := agent.DefaultCommands(tc.agent).TakesCommand(tc.command); got != tc.want {
			t.Errorf("commandRouting(%q, %q) = %v, want %v", tc.agent, tc.command, got, tc.want)
		}
	}
}
