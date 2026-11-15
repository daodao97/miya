package controller

import (
	"runtime"
	"strings"
	"time"

	"miya/internal/agent/cliupdate"
	"miya/internal/prefs"
	"miya/internal/processenv"
	"miya/internal/uiruntime"
)

func (c *Controller) AgentEnvironment() cliupdate.Environment {
	if c.Remote != nil {
		return c.agentEnvironment
	}
	return cliupdate.Environment{Platform: runtime.GOOS, WSL: processenv.AgentWSL(), Busy: c.ServiceBusy()}
}
func (c *Controller) SetAgentEnvironment(distribution string) {
	distribution = strings.TrimSpace(distribution)
	if c.Remote != nil {
		c.forward("agentEnvironment", distribution)
		return
	}
	if runtime.GOOS != "windows" || c.ServiceBusy() || strings.ContainsAny(distribution, "\r\n\x00") || distribution == processenv.AgentWSL() {
		return
	}
	prefs.Save("agentWSL", distribution)
	c.agentUpdates = nil
	c.agentUpdatesAt = time.Time{}
	c.forgetInstalls()
	c.applyClaudeCommand(c.ClaudeCommand)
	c.applyPiCommand(c.Pi.Command)
	// Only idle sessions can change environment. Invalidate the previous client
	// generation before closing it, so delayed events cannot overwrite new state.
	c.connectionID++
	if c.connectionCancel != nil {
		c.connectionCancel()
	}
	if c.Client != nil {
		old := c.Client
		c.Client = nil
		go old.Close()
	}
	c.Connecting = false
	c.SetReady(false)
	c.CheckAgentUpdates()
	c.Connect()
	uiruntime.Invalidate()
}
