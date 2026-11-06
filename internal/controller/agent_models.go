package controller

import (
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
)

func (c *Controller) NoteClaudeModel(model string) {
	if model == "" || c.ClaudeModel != "" || model == c.claudeDefaultModel {
		return
	}
	c.claudeDefaultModel = model
	prefs.Save("claudeDefaultModel", model)
	uiruntime.Invalidate()
}

func (c *Controller) setClaudeModel(v string) {
	c.ClaudeModel = v
	if c.Remote != nil {
		c.forward("claudeModel", v)
		uiruntime.Invalidate()
		return
	}
	prefs.Save("claudeModel", v)
	uiruntime.Invalidate()
}

func (c *Controller) setClaudeEffort(v string) {
	c.ClaudeEffort = v
	if c.Remote != nil {
		c.forward("claudeEffort", v)
		uiruntime.Invalidate()
		return
	}
	prefs.Save("claudeEffort", v)
	uiruntime.Invalidate()
}

func (c *Controller) setCodexEffort(v string) {
	c.SetEffort(v)
	if c.Remote == nil {
		prefs.Save("codexEffort:"+c.Model(), v)
	}
}

func SavedCodexEffort(m codex.Model) string {
	saved := prefs.Load("codexEffort:" + m.Model)
	for _, option := range m.SupportedReasoningEfforts {
		if option.ReasoningEffort == saved {
			return saved
		}
	}
	return m.DefaultReasoningEffort
}

// agentSpeedBadge supplies the composer's optional speed indicator.
func (c *Controller) AgentSpeedBadge(agent string) string {
	if agent == appmodel.AgentCodex && c.fastMode {
		return "2x"
	}
	return ""
}
