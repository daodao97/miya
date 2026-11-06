package controller

import (
	"slices"

	"miya/internal/appmodel"
	"miya/internal/claude"
	"miya/internal/codex"
	"miya/internal/prefs"
	"miya/internal/textutil"
	"miya/internal/uiruntime"
)

func (b CodexBackend) ModelChoices() []string { c := b.C; return c.modelNames() }

func (b CodexBackend) ModelLabel(v string) string {
	return textutil.FirstNonEmpty(v, tr("Default model"))
}

func (b CodexBackend) EffortChoices() []string { c := b.C; return c.efforts() }

func (b CodexBackend) Model() string {
	c := b.C
	if c.Model == nil {
		return ""
	}
	return c.Model()
}

func (b CodexBackend) Effort() string {
	c := b.C
	if c.Effort == nil {
		return ""
	}
	return c.Effort()
}

func (b CodexBackend) SetModel(v string) { c := b.C; c.SelectModel(v) }

func (b CodexBackend) SetEffort(v string) { c := b.C; c.setCodexEffort(v) }

// Claude Code takes a model's alias or full name and an effort level.
var claudeModelChoices = []string{"", "claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}

var claudeEffortChoices = []string{"", "low", "medium", "high", "xhigh", "max"}

func (b claudeBackend) ModelChoices() []string {
	c := b.C
	out := append([]string(nil), claudeModelChoices...)
	if m := c.ClaudeModel; m != "" && !slices.Contains(out, m) {
		out = append(out, m)
	}
	return out
}

// ModelLabel names the default by the model Claude Code last reported.
func (b claudeBackend) ModelLabel(v string) string {
	c := b.C
	if v != "" {
		return claude.ModelName(v)
	}
	if c.claudeDefaultModel != "" {
		return tr("Default · ") + claude.ModelName(c.claudeDefaultModel)
	}
	return tr("Default model")
}

// noteClaudeModel records the model a run without a chosen one used.
func (c *Controller) NoteClaudeModel(model string) {
	if model == "" || c.ClaudeModel != "" || model == c.claudeDefaultModel {
		return
	}
	c.claudeDefaultModel = model
	prefs.Save("claudeDefaultModel", model)
	uiruntime.Invalidate()
}

func (b claudeBackend) EffortChoices() []string { return claudeEffortChoices }

func (b claudeBackend) Model() string { c := b.C; return c.ClaudeModel }

func (b claudeBackend) Effort() string { c := b.C; return c.ClaudeEffort }

func (b claudeBackend) SetModel(v string) { c := b.C; c.setClaudeModel(v) }

func (b claudeBackend) SetEffort(v string) { c := b.C; c.setClaudeEffort(v) }

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
