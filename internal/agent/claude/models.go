package claude

import (
	"slices"
	"strings"

	"miya/internal/agent"
	claude "miya/internal/agent/claude/native"
	"miya/internal/textutil"
)

// ModelChoices are the models Claude Code reports, or Miya's own list until
// it has reported them. "", following Claude Code's default, takes the place
// of the model the default runs, so that model is listed once and checked
// while it is the default; until that model is known "" comes first.
func (b Adapter) ModelChoices() []string {
	b.host.RefreshModels()
	s := b.host.Snapshot()
	type entry struct{ value, resolved string }
	var entries []entry
	if len(s.Models) == 0 {
		for _, v := range claudeModelChoices {
			entries = append(entries, entry{v, v})
		}
	}
	for _, m := range s.Models {
		if v := modelValue(m); v != "" && offered(m) {
			entries = append(entries, entry{v, m.ResolvedModel})
		}
	}
	def := defaultResolved(s)
	// chosenDefault is a model chosen by name that runs what the default runs.
	chosenDefault := false
	for _, e := range entries {
		if def != "" && e.value == s.Model && e.resolved == def {
			chosenDefault = true
		}
	}
	var out []string
	placed := false
	for _, e := range entries {
		if slices.Contains(out, e.value) {
			continue
		}
		if def == "" || e.resolved != def {
			out = append(out, e.value)
			continue
		}
		switch {
		case chosenDefault && e.value == s.Model:
			out, placed = append(out, e.value), true
		case !chosenDefault && !placed:
			out, placed = append(out, ""), true
		}
	}
	if !placed {
		out = append([]string{""}, out...)
	}
	if s.Model != "" && !slices.Contains(out, s.Model) {
		out = append(out, s.Model)
	}
	return out
}

// offered reports whether the picker lists m: Claude Code's own Claude
// models, not the gateway and routing models a user configures, which it
// reports as provider/model.
func offered(m claude.Model) bool {
	return !strings.Contains(m.Value, "/") && strings.HasPrefix(m.ResolvedModel, "claude-")
}

// defaultResolved is the model Claude Code runs when none is chosen.
func defaultResolved(s State) string {
	if m, ok := reported(s.Models, ""); ok && m.ResolvedModel != "" {
		return m.ResolvedModel
	}
	return s.DefaultModel
}

// modelValue is what Miya passes Claude Code for m, "" for its default.
func modelValue(m claude.Model) string {
	if m.Value == "default" {
		return ""
	}
	return m.Value
}

func reported(models []claude.Model, v string) (claude.Model, bool) {
	for _, m := range models {
		if modelValue(m) == v {
			return m, true
		}
	}
	return claude.Model{}, false
}

// reportedName names a reported model by the model it runs when Miya knows
// that one, otherwise as Claude Code names it.
func reportedName(m claude.Model) string {
	if n := claude.ModelName(m.ResolvedModel); n != m.ResolvedModel {
		return n
	}
	// A Claude model newer than Miya: Claude Code describes it as
	// "Fable 5.2 · …", where the display name is only "Fable".
	if strings.HasPrefix(m.ResolvedModel, "claude-") {
		if name, _, ok := strings.Cut(m.Description, " · "); ok && strings.TrimSpace(name) != "" {
			return "Claude " + strings.TrimSpace(name)
		}
	}
	return textutil.FirstNonEmpty(m.DisplayName, m.Value)
}

// ModelLabel names the default by the model it runs, as Claude Code last
// reported it; the list shows it in that model's place.
func (b Adapter) ModelLabel(v string) string {
	s := b.host.Snapshot()
	if v != "" {
		if m, ok := reported(s.Models, v); ok {
			return reportedName(m)
		}
		return claude.ModelName(v)
	}
	def := defaultResolved(s)
	for _, m := range s.Models {
		if m.Value != "default" && offered(m) && m.ResolvedModel == def {
			return reportedName(m)
		}
	}
	if def != "" {
		return claude.ModelName(def)
	}
	return tr("Default model")
}

func (b Adapter) EffortChoices() []string { return b.efforts(b.Model()) }

// efforts are the efforts model v takes, as Claude Code reports them.
func (b Adapter) efforts(v string) []string {
	if m, ok := reported(b.host.Snapshot().Models, v); ok {
		if !m.SupportsEffort {
			return []string{""}
		}
		if len(m.SupportedEffortLevels) > 0 {
			return append([]string{""}, m.SupportedEffortLevels...)
		}
	}
	return append([]string(nil), claudeEffortChoices...)
}

func (b Adapter) Model() string { return b.host.Snapshot().Model }

func (b Adapter) Effort() string { return b.host.Snapshot().Effort }

func (b Adapter) SetModel(v string) { b.host.SetModel(v) }

func (b Adapter) SetEffort(v string) { b.host.SetEffort(v) }

func (b Adapter) ModelOptions() []agent.ModelOption {
	var out []agent.ModelOption
	for _, value := range b.ModelChoices() {
		out = append(out, agent.ModelOption{Value: value, Label: b.ModelLabel(value), Efforts: b.efforts(value)})
	}
	return out
}

// claudeModelChoices are offered until Claude Code reports its models.
var claudeModelChoices = []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
var claudeEffortChoices = []string{"", "low", "medium", "high", "xhigh", "max"}
