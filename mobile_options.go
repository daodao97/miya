package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

func (m *mobileApp) agentOptions(agent string) phoneAgentOptions {
	for _, a := range m.state.Agents {
		if a.Agent == agent {
			return a
		}
	}
	return phoneAgentOptions{Agent: agent, Current: remote.SendOptions{Model: m.state.Model, Effort: m.state.Effort, Policy: "on-request"}}
}
func (m *mobileApp) sendOptions(agent string) remote.SendOptions {
	if o, ok := m.options[agent]; ok {
		return o
	}
	o := m.agentOptions(agent).Current
	if m.open != 0 {
		t := m.view.c.current()
		if threadAgent(t) == agent && t.PermissionPolicy != "" {
			o.Policy = t.PermissionPolicy
		}
	}
	return o
}
func (m *mobileApp) saveOptions() {
	dir := firstNonEmpty(m.cacheDir, m.dataDir)
	if dir == "" {
		return
	}
	data, err := json.Marshal(m.options)
	if err != nil {
		return
	}
	if os.MkdirAll(dir, 0700) == nil {
		_ = os.WriteFile(filepath.Join(dir, "message-options.json"), data, 0600)
	}
}

func (m *mobileApp) loadDesktopOptions() {
	m.options = nil
	if m.cacheDir == "" {
		return
	}
	if data, err := os.ReadFile(filepath.Join(m.cacheDir, "message-options.json")); err == nil {
		_ = json.Unmarshal(data, &m.options)
	}
}
func (m *mobileApp) setOptions(agent string, o remote.SendOptions) {
	if m.options == nil {
		m.options = map[string]remote.SendOptions{}
	}
	m.options[agent] = o
	m.saveOptions()
}
func (m *mobileApp) openOptions(agent string) { m.optionsAgent = agent; m.router().Push("/options") }

func (m *mobileApp) optionsView(c *ui.Context) {
	p := m.pal
	agent := m.optionsAgent
	a := m.agentOptions(agent)
	o := m.sendOptions(agent)
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/options")
		ui.Text(c, tr("Conversation settings")).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(14).Children(func() {
			ui.Text(c, agentName(agent)).FontSize(m.font(20)).FontWeight(600)
			ui.Text(c, tr("Applies to new messages sent from your phone. Desktop defaults are unchanged.")).FontSize(m.font(13)).TextColor(p.muted)
			if m.pill(c, "defaults", tr("Use desktop defaults"), false).Clicked() {
				delete(m.options, agent)
				m.saveOptions()
			}
			ui.Text(c, tr("Model")).FontSize(m.font(13)).TextColor(p.muted)
			ui.Column(c).FillWidth().Background(p.surface).Radius(16).Padding(6).Role(ui.RoleRadioGroup).Label(tr("Model")).Children(func() {
				for _, model := range a.Models {
					row := m.phoneButton(c.Key("model:" + model.Value)).FillWidth().MinHeight(44).Padding(10).Gap(8).Role(ui.RoleRadio).Checked(model.Value == o.Model).Label(tr("Model ") + model.Label)
					row.Children(func() {
						ui.Text(c, model.Label).Grow(1).MinWidth(0)
						if model.Value == o.Model {
							icon(c, "check", 18, p.ink)
						}
					})
					if row.Clicked() {
						o.Model, o.Effort = model.Value, model.DefaultEffort
						m.setOptions(agent, o)
					}
				}
				if len(a.Models) == 0 {
					ui.Text(c, tr("Connect to your desktop to select a model")).Padding(10).TextColor(p.muted)
				}
			})
			ui.Text(c, tr("Reasoning effort")).FontSize(m.font(13)).TextColor(p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(8).Role(ui.RoleRadioGroup).Label(tr("Reasoning effort")).Children(func() {
				choices := []string{""}
				for _, model := range a.Models {
					if model.Value == o.Model {
						for _, e := range model.Efforts {
							if e != "" {
								choices = append(choices, e)
							}
						}
					}
				}
				for _, e := range choices {
					label := tr(strings.ToUpper(e))
					if e == "" {
						label = tr("Model default")
					}
					if m.pill(c, "effort:"+e, label, e == o.Effort).Role(ui.RoleRadio).Checked(e == o.Effort).Label(tr("Effort ") + label).Clicked() {
						o.Effort = e
						m.setOptions(agent, o)
					}
				}
			})
			ui.Text(c, tr("Permission mode")).FontSize(m.font(13)).TextColor(p.muted)
			ui.Column(c).FillWidth().Gap(14).Role(ui.RoleRadioGroup).Label(tr("Permission mode")).Children(func() {
				for _, policy := range []struct{ value, label, detail string }{{"on-request", tr("Ask when needed"), tr("Confirm sensitive actions yourself")}, {"on-failure", tr("Automatic approval"), tr("Automatic approval handles requests and asks you when needed")}, {"never", tr("Full access"), tr("Allow the agent to act without asking")}} {
					b := m.phoneButton(c.Key("policy:" + policy.value)).FillWidth().MinHeight(56).Padding(12).Gap(12).Background(p.surface).Radius(14).Role(ui.RoleRadio).Checked(policy.value == o.Policy).Label(policy.label)
					b.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, policy.label).FontWeight(500)
							ui.Text(c, policy.detail).FontSize(m.font(12)).TextColor(p.muted)
						})
						if policy.value == o.Policy {
							icon(c, "check", 18, p.ink)
						}
					})
					if b.Clicked() {
						o.Policy = policy.value
						m.setOptions(agent, o)
					}
				}
			})
		})
	})
}
