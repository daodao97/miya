package controller

import (
	"sort"
	"strings"
	"time"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	claude "miya/internal/agent/claude/native"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
)

// agentSwitches are the agents the user turned on or off. An agent without
// a choice follows its catalog default.
type agentSwitches map[string]bool

const agentSwitchesKey = "agentEnabled"

// loadAgentSwitches reads "id=1,id=0" from the settings file.
func loadAgentSwitches() agentSwitches { return parseAgentSwitches(prefs.Load(agentSwitchesKey)) }

func parseAgentSwitches(v string) agentSwitches {
	out := agentSwitches{}
	for _, part := range strings.Split(v, ",") {
		id, on, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && id != "" {
			out[id] = on == "1"
		}
	}
	return out
}

func (s agentSwitches) String() string {
	var parts []string
	for id, on := range s {
		v := "0"
		if on {
			v = "1"
		}
		parts = append(parts, id+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// agentInstall is whether an agent's CLI was found, and where.
type agentInstall struct {
	Path, Problem string
	at            time.Time
}

// AgentInstall finds an agent's CLI: by its launch command when Miya has
// one for it, otherwise by its executable on PATH or in the usual install
// directories.
func (c *Controller) AgentInstall(id string) (path, problem string) {
	switch id {
	case agentmeta.Claude:
		return c.ClaudeBinary, c.ClaudeError
	case agentmeta.Pi:
		return c.Pi.Binary, c.Pi.Error
	}
	if got, ok := c.installs[id]; ok && time.Since(got.at) < 30*time.Second {
		return got.Path, got.Problem
	}
	d, ok := agentmeta.Lookup(id)
	if !ok || d.Command == "" {
		return "", ""
	}
	cmd, err := claude.ParseCommandFor("", d.Command)
	got := agentInstall{Path: cmd.Path, at: time.Now()}
	if err != nil {
		got.Problem = err.Error()
	}
	// Codex may run from a binary Miya was given rather than one on PATH.
	if got.Path == "" && id == agentmeta.Codex && c.Ready != nil && c.Ready() {
		got.Path, got.Problem = "codex", ""
	}
	if c.installs == nil {
		c.installs = map[string]agentInstall{}
	}
	c.installs[id] = got
	return got.Path, got.Problem
}

// forgetInstalls makes the next check look for the CLIs again.
func (c *Controller) forgetInstalls() { c.installs = nil }

// RecheckAgents looks for every agent's CLI again, as after installing one.
func (c *Controller) RecheckAgents() {
	c.forgetInstalls()
	if c.Remote == nil {
		c.applyPiCommand(c.Pi.Command)
		c.applyClaudeCommand(c.ClaudeCommand)
	} else {
		c.forward("recheckAgents", nil)
	}
	uiruntime.Invalidate()
}

// AgentEnabled is whether new conversations may use the agent.
func (c *Controller) AgentEnabled(id string) bool {
	if on, ok := c.agents[id]; ok {
		return on
	}
	d, ok := agentmeta.Lookup(id)
	if !ok {
		return false
	}
	if d.OptIn {
		path, _ := c.AgentInstall(id)
		return path != ""
	}
	return true
}

// EnabledAgents lists the enabled agents in catalog order; when none is, the
// default agent stays usable.
func (c *Controller) EnabledAgents() []string {
	var out []string
	for _, id := range agentmeta.IDs() {
		if c.AgentEnabled(id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		out = []string{agentmeta.Default().ID}
	}
	return out
}

// SetAgentEnabled turns an agent on or off for new conversations. Existing
// conversations keep their agent. The last enabled agent cannot be turned off.
func (c *Controller) SetAgentEnabled(id string, on bool) {
	if _, ok := agentmeta.Lookup(id); !ok {
		return
	}
	if !on && c.AgentEnabled(id) && len(c.EnabledAgents()) == 1 {
		return
	}
	if c.agents == nil {
		c.agents = agentSwitches{}
	}
	c.agents[id] = on
	if c.Remote != nil {
		c.forward("agentEnabled", map[string]any{"id": id, "enabled": on})
	} else {
		prefs.Save(agentSwitchesKey, c.agents.String())
	}
	if !on && c.Agent != nil && c.Agent() == id && c.SetAgent != nil {
		c.SetAgent(c.EnabledAgents()[0])
	}
	uiruntime.Invalidate()
}

// AgentSwitches are the user's explicit choices, for the service state.
func (c *Controller) AgentSwitches() map[string]bool {
	out := map[string]bool{}
	for id, on := range c.agents {
		out[id] = on
	}
	return out
}

// Agents is every agent with whether it is installed and enabled.
func (c *Controller) Agents() []agent.Availability {
	var out []agent.Availability
	for _, d := range agentmeta.All() {
		path, problem := c.AgentInstall(d.ID)
		out = append(out, agent.Availability{ID: d.ID, Name: d.Name, Path: path, Problem: problem, Enabled: c.AgentEnabled(d.ID)})
	}
	return out
}
