package controller

import (
	"testing"

	agentmeta "miya/internal/agent/catalog"
)

func TestAgentSwitchesRoundTrip(t *testing.T) {
	s := parseAgentSwitches("pi=1, codex=0,bogus")
	if !s["pi"] || s["codex"] || len(s) != 2 {
		t.Fatalf("switches = %v", s)
	}
	if got := s.String(); got != "codex=0,pi=1" {
		t.Fatalf("encoded = %q", got)
	}
}

func TestPiIsEnabledOnlyOnceInstalledUnlessChosen(t *testing.T) {
	c := &Controller{}
	c.Pi.Error = "pi not found"
	if c.AgentEnabled(agentmeta.Pi) {
		t.Fatal("pi should be off while not installed")
	}
	c.Pi.Binary, c.Pi.Error = "/opt/homebrew/bin/pi", ""
	if !c.AgentEnabled(agentmeta.Pi) {
		t.Fatal("pi should be on once installed")
	}
	c.agents = agentSwitches{agentmeta.Pi: false}
	if c.AgentEnabled(agentmeta.Pi) {
		t.Fatal("the user's choice should win")
	}
	if !c.AgentEnabled(agentmeta.Claude) || !c.AgentEnabled(agentmeta.Codex) {
		t.Fatal("built-in agents default to on")
	}
}

func TestTheLastEnabledAgentStaysOn(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := &Controller{agents: agentSwitches{agentmeta.Codex: false, agentmeta.Pi: false}}
	c.SetAgentEnabled(agentmeta.Claude, false)
	if !c.AgentEnabled(agentmeta.Claude) {
		t.Fatal("the last enabled agent was turned off")
	}
	c.SetAgentEnabled(agentmeta.Pi, true)
	c.SetAgentEnabled(agentmeta.Claude, false)
	if c.AgentEnabled(agentmeta.Claude) || len(c.EnabledAgents()) != 1 || c.EnabledAgents()[0] != agentmeta.Pi {
		t.Fatalf("enabled = %v", c.EnabledAgents())
	}
	if got := loadAgentSwitches(); got[agentmeta.Claude] || !got[agentmeta.Pi] {
		t.Fatalf("saved switches = %v", got)
	}
}
