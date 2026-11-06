package claude

import (
	"slices"

	"miya/internal/agent"
	claude "miya/internal/agent/claude/native"
)

func (b Adapter) ModelChoices() []string {
	c := b.host
	out := append([]string(nil), claudeModelChoices...)
	if m := c.Snapshot().Model; m != "" && !slices.Contains(out, m) {
		out = append(out, m)
	}
	return out
}

// ModelLabel names the default by the model Claude Code last reported.
func (b Adapter) ModelLabel(v string) string {
	c := b.host
	if v != "" {
		return claude.ModelName(v)
	}
	if c.Snapshot().DefaultModel != "" {
		return tr("Default · ") + claude.ModelName(c.Snapshot().DefaultModel)
	}
	return tr("Default model")
}

func (b Adapter) EffortChoices() []string { return claudeEffortChoices }

func (b Adapter) Model() string { return b.host.Snapshot().Model }

func (b Adapter) Effort() string { return b.host.Snapshot().Effort }

func (b Adapter) SetModel(v string) { b.host.SetModel(v) }

func (b Adapter) SetEffort(v string) { b.host.SetEffort(v) }

func (b Adapter) ModelOptions() []agent.ModelOption {
	var out []agent.ModelOption
	for _, value := range b.ModelChoices() {
		out = append(out, agent.ModelOption{Value: value, Label: b.ModelLabel(value), Efforts: append([]string(nil), b.EffortChoices()...)})
	}
	return out
}

var claudeModelChoices = []string{"", "claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
var claudeEffortChoices = []string{"", "low", "medium", "high", "xhigh", "max"}
