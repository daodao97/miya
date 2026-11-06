package controller

import (
	claudeagent "miya/internal/agent/claude"
	claudenative "miya/internal/agent/claude/native"
	"miya/internal/interaction"
	"miya/internal/model"
)

func (c *Controller) claudeAdapter() *claudeagent.Adapter {
	return claudeagent.New(claudeAgentHost{agentWorkspace{c}})
}
func (c *Controller) LoadClaudeHistory(t model.Thread) { c.claudeAdapter().LoadHistory(t) }
func (c *Controller) FinishClaude(id int, cwd string, err error) {
	c.claudeAdapter().Finish(id, cwd, err)
}
func (c *Controller) HandleClaude(id int, f claudenative.Frame) { c.claudeAdapter().Handle(id, f) }
func (c *Controller) RespondClaude(a interaction.Request, r any) bool {
	return c.claudeAdapter().RespondLegacy(a, r)
}
