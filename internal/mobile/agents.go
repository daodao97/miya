package mobile

import (
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/model"
)

func (m *App) agentDescriptors() []agentmeta.Descriptor {
	var out []agentmeta.Descriptor
	for _, option := range m.State.Agents {
		d := option.Descriptor
		if d.ID == "" {
			d = agentmeta.For(option.Agent)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return agentmeta.All()
	}
	return out
}
func (m *App) agentDescriptor(id string) agentmeta.Descriptor {
	for _, d := range m.agentDescriptors() {
		if d.ID == id {
			return d
		}
	}
	return agentmeta.For(id)
}
func (m *App) defaultAgent() string { return agentmeta.Default().ID }
func (m *App) commandChoices(id, project string) ([]model.ComposerChoice, bool) {
	choices, known := m.Commands[id+"\x00"+project]
	if !known && id == agentmeta.LegacyCommandAgent() {
		choices, known = m.Commands[project]
	}
	return choices, known
}
