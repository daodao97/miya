package codex

import (
	"slices"

	"miya/internal/agent"
	"miya/internal/textutil"
)

func (b Adapter) ModelChoices() []string {
	state := b.host.Snapshot()
	out := []string{}
	for _, m := range state.Models {
		out = append(out, m.Model)
	}
	if state.Model != "" && !slices.Contains(out, state.Model) {
		out = append(out, state.Model)
	}
	return out
}

func (b Adapter) ModelLabel(v string) string {
	return textutil.FirstNonEmpty(v, tr("Default model"))
}

func (b Adapter) EffortChoices() []string {
	state := b.host.Snapshot()
	for _, m := range state.Models {
		if m.Model == state.Model {
			out := []string{}
			for _, e := range m.SupportedReasoningEfforts {
				out = append(out, e.ReasoningEffort)
			}
			return out
		}
	}
	return nil
}

func (b Adapter) Model() string { return b.host.Snapshot().Model }

func (b Adapter) Effort() string { return b.host.Snapshot().Effort }

func (b Adapter) SetModel(v string) { b.host.SelectModel(v) }

func (b Adapter) SetEffort(v string) { b.host.SelectEffort(v) }

func (b Adapter) ModelOptions() []agent.ModelOption {
	c := b.host
	choices := []string{}
	if c.Snapshot().ModelsKnown {
		choices = b.ModelChoices()
	} else if b.Model() != "" {
		choices = append(choices, b.Model())
	}
	if !slices.Contains(choices, "") {
		choices = append([]string{""}, choices...)
	}
	var out []agent.ModelOption
	for _, value := range choices {
		option := agent.ModelOption{Value: value, Label: b.ModelLabel(value)}
		if c.Snapshot().ModelsKnown {
			for _, m := range c.Snapshot().Models {
				if m.Model == value || value == "" && m.IsDefault {
					option.DefaultEffort = m.DefaultReasoningEffort
					for _, e := range m.SupportedReasoningEfforts {
						option.Efforts = append(option.Efforts, e.ReasoningEffort)
					}
					break
				}
			}
		}
		if len(option.Efforts) == 0 && value == b.Model() && b.Effort() != "" {
			option.Efforts = []string{b.Effort()}
			option.DefaultEffort = b.Effort()
		}
		out = append(out, option)
	}
	return out
}
