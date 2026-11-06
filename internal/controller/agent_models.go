package controller

import (
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	textutil "miya/internal/textutil"
	uiruntime "miya/internal/uiruntime"
	slices "slices"
)

func (CodexBackend) ModelChoices(c *Controller) []string { return c.modelNames() }

func (CodexBackend) ModelLabel(_ *Controller, v string) string {
	return textutil.FirstNonEmpty(v, tr("Default model"))
}

func (CodexBackend) EffortChoices(c *Controller) []string { return c.efforts() }

func (CodexBackend) Model(c *Controller) string {
	if c.Model == nil {
		return ""
	}
	return c.Model()
}

func (CodexBackend) Effort(c *Controller) string {
	if c.Effort == nil {
		return ""
	}
	return c.Effort()
}

func (CodexBackend) SetModel(c *Controller, v string) { c.SelectModel(v) }

func (CodexBackend) SetEffort(c *Controller, v string) { c.setCodexEffort(v) }

// Claude Code takes a model's alias or full name and an effort level.
var claudeModelChoices = []string{"", "claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}

var claudeEffortChoices = []string{"", "low", "medium", "high", "xhigh", "max"}

func (claudeBackend) ModelChoices(c *Controller) []string {
	out := append([]string(nil), claudeModelChoices...)
	if m := c.ClaudeModel; m != "" && !slices.Contains(out, m) {
		out = append(out, m)
	}
	return out
}

// ModelLabel names the default by the model Claude Code last reported.
func (claudeBackend) ModelLabel(c *Controller, v string) string {
	if v != "" {
		return appmodel.ClaudeModelName(v)
	}
	if c.claudeDefaultModel != "" {
		return tr("Default · ") + appmodel.ClaudeModelName(c.claudeDefaultModel)
	}
	return tr("Default model")
}

// noteClaudeModel records the model a run without a chosen one used.
func (c *Controller) NoteClaudeModel(model string) {
	if model == "" || c.ClaudeModel != "" || model == c.claudeDefaultModel {
		return
	}
	c.claudeDefaultModel = model
	appmodel.PersistSetting("claudeDefaultModel", model)
	uiruntime.Invalidate()
}

func (claudeBackend) EffortChoices(*Controller) []string { return claudeEffortChoices }

func (claudeBackend) Model(c *Controller) string { return c.ClaudeModel }

func (claudeBackend) Effort(c *Controller) string { return c.ClaudeEffort }

func (claudeBackend) SetModel(c *Controller, v string) { c.setClaudeModel(v) }

func (claudeBackend) SetEffort(c *Controller, v string) { c.setClaudeEffort(v) }

func (c *Controller) setClaudeModel(v string) {
	c.ClaudeModel = v
	if c.Remote != nil {
		c.forward("claudeModel", v)
		uiruntime.Invalidate()
		return
	}
	appmodel.PersistSetting("claudeModel", v)
	uiruntime.Invalidate()
}

func (c *Controller) setClaudeEffort(v string) {
	c.ClaudeEffort = v
	if c.Remote != nil {
		c.forward("claudeEffort", v)
		uiruntime.Invalidate()
		return
	}
	appmodel.PersistSetting("claudeEffort", v)
	uiruntime.Invalidate()
}

func (c *Controller) setCodexEffort(v string) {
	c.SetEffort(v)
	if c.Remote == nil {
		appmodel.PersistSetting("codexEffort:"+c.Model(), v)
	}
}

func SavedCodexEffort(m codex.Model) string {
	saved := appmodel.LoadSetting("codexEffort:" + m.Model)
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
