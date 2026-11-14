package client

import (
	"encoding/json"
	"os"
	"path/filepath"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
)

func (m *Client) agentOptions(agent string) phoneproto.AgentOptions {
	for _, a := range m.State.Agents {
		if a.Agent == agent {
			return a
		}
	}
	return phoneproto.AgentOptions{Agent: agent, Current: remote.SendOptions{Model: m.State.Model, Effort: m.State.Effort, Policy: "on-request"}}
}

func (m *Client) SendOptions(agent string) remote.SendOptions {
	if o, ok := m.Options[m.optionsKey(agent)]; ok {
		return o
	}
	o := m.agentOptions(agent).Current
	if m.Open != 0 {
		t := m.Viewer.Current()
		if model.ThreadAgent(t) == agent && t.PermissionPolicy != "" {
			o.Policy = t.PermissionPolicy
		}
	}
	return o
}

func (m *Client) SaveOptions() {
	dir := textutil.FirstNonEmpty(m.CacheDir, m.DataDir)
	if dir == "" {
		return
	}
	data, err := json.Marshal(m.Options)
	if err != nil {
		return
	}
	if os.MkdirAll(dir, 0700) == nil {
		_ = os.WriteFile(filepath.Join(dir, "message-options.json"), data, 0600)
	}
}

func (m *Client) loadDesktopOptions() {
	m.Options = nil
	if m.CacheDir == "" {
		return
	}
	if data, err := os.ReadFile(filepath.Join(m.CacheDir, "message-options.json")); err == nil {
		_ = json.Unmarshal(data, &m.Options)
	}
}

func (m *Client) setOptions(agent string, o remote.SendOptions) {
	if m.Options == nil {
		m.Options = map[string]remote.SendOptions{}
	}
	m.Options[m.optionsKey(agent)] = o
	m.SaveOptions()
}

func (m *Client) optionsKey(agent string) string {
	return agent
}
