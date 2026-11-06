package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/model"
	"miya/internal/remote"
	"miya/internal/textutil"

	"github.com/egoist/mygo/ui"
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
		t := m.viewer.current()
		if model.ThreadAgent(t) == agent && t.PermissionPolicy != "" {
			o.Policy = t.PermissionPolicy
		}
	}
	return o
}
func (m *mobileApp) saveOptions() {
	dir := textutil.FirstNonEmpty(m.cacheDir, m.dataDir)
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
	_, customized := m.options[agent]
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/options")
		ui.Text(c, tr("Conversation settings")).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(8, 16, 32, 16).Gap(28).Children(func() {
			m.optionsHeader(c.Key("header"), agent, customized)
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				m.optionsTitle(c, tr("Model"))
				ui.Column(c).FillWidth().Background(p.surface).Radius(16).Role(ui.RoleRadioGroup).Label(tr("Model")).Children(func() {
					for i, model := range a.Models {
						if i > 0 {
							m.optionsDivider(c.Key("model-divider:"+model.Value), 16)
						}
						selected := model.Value == o.Model
						row := m.phoneButton(c.Key("model:" + model.Value)).FillWidth().MinHeight(50).PaddingX(16).Gap(12).Radius(0).Role(ui.RoleRadio).Checked(selected).Label(tr("Model ") + model.Label)
						row.Children(func() {
							name := ui.Text(c, model.Label).FontSize(m.font(16)).Grow(1).MinWidth(0)
							if selected {
								name.FontWeight(600)
								icon(c, "check", 18, p.link)
							}
						})
						if row.Clicked() {
							o.Model, o.Effort = model.Value, model.DefaultEffort
							m.setOptions(agent, o)
						}
					}
					if len(a.Models) == 0 {
						ui.Text(c, tr("Connect to your desktop to select a model")).FontSize(m.font(14)).Padding(16).TextColor(p.muted)
					}
				})
			})
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				m.optionsTitle(c, tr("Reasoning effort"))
				ui.Row(c).FillWidth().Wrap().Gap(4).Padding(4).Radius(14).Background(p.surface).Role(ui.RoleRadioGroup).Label(tr("Reasoning effort")).Children(func() {
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
						selected := e == o.Effort
						seg := m.phoneButton(c.Key("effort:" + e)).Grow(1).MinHeight(40).PaddingX(12).Radius(10).Justify(ui.Center).Role(ui.RoleRadio).Checked(selected).Label(tr("Effort ") + label)
						if selected {
							seg.Background(p.bg)
						}
						seg.Children(func() {
							text := ui.Text(c, label).FontSize(m.font(14)).TextColor(p.muted)
							if selected {
								text.FontWeight(600).TextColor(p.ink)
							}
						})
						if seg.Clicked() {
							o.Effort = e
							m.setOptions(agent, o)
						}
					}
				})
			})
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				m.optionsTitle(c, tr("Permission mode"))
				policies := []struct {
					value, label, detail, icon string
					tint                       ui.Color
				}{
					{"on-request", tr("Ask when needed"), tr("Confirm sensitive actions yourself"), "hand-stop", p.link},
					{"on-failure", tr("Automatic approval"), tr("Automatic approval handles requests and asks you when needed"), "shield-check", p.ok},
					{"never", tr("Full access"), tr("Allow the agent to act without asking"), "lock-open", p.warn},
				}
				ui.Column(c).FillWidth().Background(p.surface).Radius(16).Role(ui.RoleRadioGroup).Label(tr("Permission mode")).Children(func() {
					for i, policy := range policies {
						if i > 0 {
							m.optionsDivider(c.Key("policy-divider:"+policy.value), 64)
						}
						selected := policy.value == o.Policy
						b := m.phoneButton(c.Key("policy:"+policy.value)).FillWidth().MinHeight(64).Padding(12, 16).Gap(14).Radius(0).Role(ui.RoleRadio).Checked(selected).Label(policy.label)
						b.Children(func() {
							ui.Row(c).Size(34, 34).Shrink(0).Radius(10).Background(p.surface.Mix(policy.tint, 0.16)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
								icon(c, policy.icon, 18, policy.tint)
							})
							ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
								ui.Text(c, policy.label).FontSize(m.font(16)).FontWeight(500)
								ui.Text(c, policy.detail).FontSize(m.font(13)).TextColor(p.muted).LineHeight(1.35)
							})
							m.radioMark(c, selected)
						})
						if b.Clicked() {
							o.Policy = policy.value
							m.setOptions(agent, o)
						}
					}
				})
			})
		})
	})
}

// optionsHeader names the agent being configured and offers a way back to
// the desktop defaults once the phone has its own choices.
func (m *mobileApp) optionsHeader(c *ui.Context, agent string, customized bool) {
	p := m.pal
	ui.Column(c).FillWidth().Padding(16).Gap(14).Radius(20).Background(p.surface).Children(func() {
		ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Size(52, 52).Shrink(0).Radius(16).Background(p.bg).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				agentIcon(c, agent, 30)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, agentName(agent)).FontSize(m.font(19)).FontWeight(600)
				ui.Text(c, tr("Applies to new messages sent from your phone. Desktop defaults are unchanged.")).FontSize(m.font(13)).TextColor(p.muted).LineHeight(1.4)
			})
		})
		if !customized {
			return
		}
		reset := m.phoneButton(c.Key("defaults")).FillWidth().MinHeight(44).Radius(14).Background(p.bg).Justify(ui.Center).Label(tr("Use desktop defaults"))
		reset.Children(func() {
			icon(c, "refresh", 16, p.link)
			ui.Text(c, tr("Use desktop defaults")).FontSize(m.font(14)).FontWeight(500).TextColor(p.link)
		})
		if reset.Clicked() {
			delete(m.options, agent)
			m.saveOptions()
		}
	})
}

func (m *mobileApp) optionsTitle(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(m.font(13)).FontWeight(500).TextColor(m.pal.muted).PaddingX(16)
}

// optionsDivider separates rows of a grouped list, inset to the row text.
func (m *mobileApp) optionsDivider(c *ui.Context, inset float32) {
	ui.Row(c).FillWidth().Padding(0, 0, 0, inset).Children(func() {
		ui.Box(c).Grow(1).Height(0.5).Background(m.pal.line)
	})
}

func (m *mobileApp) radioMark(c *ui.Context, on bool) {
	p := m.pal
	if on {
		ui.Row(c).Size(22, 22).Shrink(0).Radius(11).Background(p.link).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			icon(c, "check", 14, ui.RGB(255, 255, 255))
		})
		return
	}
	ui.Box(c).Size(22, 22).Shrink(0).Radius(11).Border(1.5, p.muted)
}
