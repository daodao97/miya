package main

import (
	"miya/internal/codex"
	"miya/internal/textutil"
	"slices"
)

// Each agent offers its own models and reasoning efforts; the composer's
// pickers show the current agent's, through agentBackend. An empty value
// is the agent's own default.

func (codexBackend) ModelChoices(c *chatController) []string { return c.modelNames() }

func (codexBackend) ModelLabel(_ *chatController, v string) string {
	return textutil.FirstNonEmpty(v, tr("Default model"))
}

func (codexBackend) EffortChoices(c *chatController) []string { return c.efforts() }

func (codexBackend) Model(c *chatController) string {
	if c.model == nil {
		return ""
	}
	return c.model()
}

func (codexBackend) Effort(c *chatController) string {
	if c.effort == nil {
		return ""
	}
	return c.effort()
}

func (codexBackend) SetModel(c *chatController, v string) { c.selectModel(v) }

func (codexBackend) SetEffort(c *chatController, v string) { c.setCodexEffort(v) }

// Claude Code takes a model's alias or full name and an effort level.
var (
	claudeModelChoices  = []string{"", "claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	claudeEffortChoices = []string{"", "low", "medium", "high", "xhigh", "max"}
)

func (claudeBackend) ModelChoices(c *chatController) []string {
	out := append([]string(nil), claudeModelChoices...)
	if m := c.claudeModel; m != "" && !slices.Contains(out, m) {
		out = append(out, m)
	}
	return out
}

// ModelLabel names the default by the model Claude Code last reported.
func (claudeBackend) ModelLabel(c *chatController, v string) string {
	if v != "" {
		return claudeModelName(v)
	}
	if c.claudeDefaultModel != "" {
		return tr("Default · ") + claudeModelName(c.claudeDefaultModel)
	}
	return tr("Default model")
}

// noteClaudeModel records the model a run without a chosen one used.
func (c *chatController) noteClaudeModel(model string) {
	if model == "" || c.claudeModel != "" || model == c.claudeDefaultModel {
		return
	}
	c.claudeDefaultModel = model
	persistSetting("claudeDefaultModel", model)
	invalidate()
}

func (claudeBackend) EffortChoices(*chatController) []string { return claudeEffortChoices }

func (claudeBackend) Model(c *chatController) string { return c.claudeModel }

func (claudeBackend) Effort(c *chatController) string { return c.claudeEffort }

func (claudeBackend) SetModel(c *chatController, v string) { c.setClaudeModel(v) }

func (claudeBackend) SetEffort(c *chatController, v string) { c.setClaudeEffort(v) }

func (c *chatController) setClaudeModel(v string) {
	c.claudeModel = v
	if c.remote != nil {
		c.forward("claudeModel", v)
		invalidate()
		return
	}
	persistSetting("claudeModel", v)
	invalidate()
}

func (c *chatController) setClaudeEffort(v string) {
	c.claudeEffort = v
	if c.remote != nil {
		c.forward("claudeEffort", v)
		invalidate()
		return
	}
	persistSetting("claudeEffort", v)
	invalidate()
}

func (c *chatController) setCodexEffort(v string) {
	c.setEffort(v)
	if c.remote == nil {
		persistSetting("codexEffort:"+c.model(), v)
	}
}

func savedCodexEffort(m codex.Model) string {
	saved := loadSetting("codexEffort:" + m.Model)
	for _, option := range m.SupportedReasoningEfforts {
		if option.ReasoningEffort == saved {
			return saved
		}
	}
	return m.DefaultReasoningEffort
}

// agentSpeedBadge supplies the composer's optional speed indicator.
func (c *chatController) agentSpeedBadge(agent string) string {
	if agent == agentCodex && c.fastMode {
		return "2x"
	}
	return ""
}
