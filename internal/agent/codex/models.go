package codex

import (
	"slices"

	"miya/internal/agent"
	native "miya/internal/agent/codex/native"
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
	state := b.host.Snapshot()
	choices := []string{}
	defaultModel := ""
	if state.ModelsKnown {
		choices = b.ModelChoices()
		if len(state.Models) > 0 {
			defaultModel = state.Models[0].Model
			if i := slices.IndexFunc(state.Models, func(m native.Model) bool { return m.IsDefault }); i >= 0 {
				defaultModel = state.Models[i].Model
			}
		}
	} else if state.Model != "" {
		choices = append(choices, state.Model)
	}
	if !slices.Contains(choices, "") {
		choices = append([]string{""}, choices...)
	}
	var out []agent.ModelOption
	for _, value := range choices {
		resolved := textutil.FirstNonEmpty(value, defaultModel)
		option := agent.ModelOption{Value: value, Label: b.ModelLabel(resolved)}
		if state.ModelsKnown {
			for _, m := range state.Models {
				if m.Model == resolved {
					option.DefaultEffort = m.DefaultReasoningEffort
					for _, e := range m.SupportedReasoningEfforts {
						option.Efforts = append(option.Efforts, e.ReasoningEffort)
					}
					break
				}
			}
		}
		if len(option.Efforts) == 0 && value == state.Model && state.Effort != "" {
			option.Efforts = []string{state.Effort}
			option.DefaultEffort = state.Effort
		}
		out = append(out, option)
	}
	return out
}
