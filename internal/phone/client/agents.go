package client

import (
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/chat/model"
)

func (m *Client) agentDescriptors() []agentmeta.Descriptor {
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

func (m *Client) agentDescriptor(id string) agentmeta.Descriptor {
	for _, d := range m.agentDescriptors() {
		if d.ID == id {
			return d
		}
	}
	return agentmeta.For(id)
}

func (m *Client) defaultAgent() string { return agentmeta.Default().ID }

func (m *Client) commandChoices(id, project string) ([]model.ComposerChoice, bool) {
	choices, known := m.Commands[id+"\x00"+project]
	if !known && id == agentmeta.LegacyCommandAgent() {
		choices, known = m.Commands[project]
	}
	return choices, known
}

func (m *Client) AgentDescriptors() []agentmeta.Descriptor { return m.agentDescriptors() }
func (m *Client) ComposerCommands() ([]model.ComposerChoice, bool) {
	id, project := m.Agent(), m.ComposerProject()
	if m.agentDescriptor(id).NativeCommands {
		m.requestCommands(id, project)
	}
	return m.composerCommands(id, project)
}
