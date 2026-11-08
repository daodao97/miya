package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"unicode"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (m *App) agentOptions(agent string) phoneproto.AgentOptions {
	for _, a := range m.State.Agents {
		if a.Agent == agent {
			return a
		}
	}
	return phoneproto.AgentOptions{Agent: agent, Current: remote.SendOptions{Model: m.State.Model, Effort: m.State.Effort, Policy: "on-request"}}
}

func (m *App) SendOptions(agent string) remote.SendOptions {
	if o, ok := m.Options[m.optionsKey(agent)]; ok {
		return o
	}
	o := m.agentOptions(agent).Current
	if m.Open != 0 {
		t := m.Viewer.Current()
		if t.Contact != nil && model.ThreadAgent(t) == agent {
			o.Model, o.Effort = t.LaunchModel, t.LaunchEffort
		}
		if model.ThreadAgent(t) == agent && t.PermissionPolicy != "" {
			o.Policy = t.PermissionPolicy
		}
	}
	return o
}

func (m *App) SaveOptions() {
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

func (m *App) loadDesktopOptions() {
	m.Options = nil
	if m.CacheDir == "" {
		return
	}
	if data, err := os.ReadFile(filepath.Join(m.CacheDir, "message-options.json")); err == nil {
		_ = json.Unmarshal(data, &m.Options)
	}
}

func (m *App) setOptions(agent string, o remote.SendOptions) {
	if m.Options == nil {
		m.Options = map[string]remote.SendOptions{}
	}
	m.Options[m.optionsKey(agent)] = o
	m.SaveOptions()
}

func (m *App) openOptions(agent string) { m.optionsAgent = agent; m.Router().Push("/options") }

func (m *App) optionsView(c *ui.Context) {
	agent := m.optionsAgent
	a, o := m.agentOptions(agent), m.SendOptions(agent)
	m.settingsHeader(c, "/options", tr("Conversation settings"))
	m.settingsBody(c, func() {
		label := textutil.FirstNonEmpty(o.Model, tr("Model default"))
		for _, model := range a.Models {
			if model.Value == o.Model {
				label = textutil.FirstNonEmpty(model.Label, label)
				break
			}
		}
		policy := o.Policy
		for _, choice := range mobilePermissionChoices() {
			if choice.value == o.Policy {
				policy = choice.label
				break
			}
		}
		m.settingsGroup(c.Key("choices"), appmodel.AgentName(agent), tr("Applies to new messages sent from your phone. Desktop defaults are unchanged."), func() {
			if m.settingsNavigation(c.Key("model"), tr("Model"), label).Clicked() {
				m.Router().Push("/options/model")
			}
			m.settingsSeparator(c)
			if m.settingsNavigation(c.Key("effort"), tr("Reasoning effort"), mobileEffortLabel(o.Effort)).Clicked() {
				m.Router().Push("/options/effort")
			}
			m.settingsSeparator(c)
			if m.settingsNavigation(c.Key("policy"), tr("Permission mode"), policy).Clicked() {
				m.Router().Push("/options/permissions")
			}
		})
		if _, customized := m.Options[agent]; customized {
			m.settingsGroup(c.Key("defaults"), "", "", func() {
				if m.settingsAction(c.Key("reset"), tr("Use desktop defaults")).Clicked() {
					delete(m.Options, agent)
					m.SaveOptions()
				}
			})
		}
	})
}

func mobileEffortLabel(effort string) string {
	switch effort {
	case "":
		return tr("Model default")
	case "xhigh":
		return tr("Extra high")
	case "max":
		return tr("Maximum")
	}
	label := []rune(tr(effort))
	if len(label) > 0 {
		label[0] = unicode.ToUpper(label[0])
	}
	return string(label)
}

func (m *App) optionChoice(c *ui.Context, label, detail string, selected bool) ui.Element {
	return m.settingsButton(c, label).Gap(12).Role(ui.RoleRadio).Checked(selected).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, label).FontSize(m.Font(17)).MinWidth(0).Wrap()
			if detail != "" {
				ui.Text(c, detail).FontSize(m.Font(13)).TextColor(m.Pal.muted).LineHeight(1.3).MinWidth(0).Wrap()
			}
		})
		ui.Box(c).Size(18, 18).Shrink(0).Children(func() {
			if selected {
				widgets.Icon(c, "check", 18, m.Pal.link)
			}
		})
	})
}

func (m *App) modelOptionsView(c *ui.Context) {
	agent := m.optionsAgent
	a, o := m.agentOptions(agent), m.SendOptions(agent)
	m.settingsHeader(c, "/options/model", tr("Model"))
	m.settingsBody(c, func() {
		m.settingsGroup(c, "", "", func() {
			ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(tr("Model")).Children(func() {
				for i, model := range a.Models {
					if i > 0 {
						m.settingsSeparator(c.Key("divider:" + model.Value))
					}
					row := m.optionChoice(c.Key("model:"+model.Value), model.Label, "", model.Value == o.Model).Label(tr("Model ") + model.Label)
					if row.Clicked() {
						o.Model, o.Effort = model.Value, model.DefaultEffort
						m.setOptions(agent, o)
						m.Router().Back()
					}
				}
				if len(a.Models) == 0 {
					ui.Text(c, tr("Connect to your desktop to select a model")).FontSize(m.Font(15)).Padding(16).TextColor(m.Pal.muted).Wrap()
				}
			})
		})
	})
}

func (m *App) effortOptionsView(c *ui.Context) {
	agent := m.optionsAgent
	a, o := m.agentOptions(agent), m.SendOptions(agent)
	choices := []string{""}
	for _, model := range a.Models {
		if model.Value == o.Model {
			for _, effort := range model.Efforts {
				if effort != "" {
					choices = append(choices, effort)
				}
			}
			break
		}
	}
	m.settingsHeader(c, "/options/effort", tr("Reasoning effort"))
	m.settingsBody(c, func() {
		m.settingsGroup(c, "", "", func() {
			ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(tr("Reasoning effort")).Children(func() {
				for i, effort := range choices {
					if i > 0 {
						m.settingsSeparator(c.Key("divider:" + effort))
					}
					label := mobileEffortLabel(effort)
					row := m.optionChoice(c.Key("effort:"+effort), label, "", effort == o.Effort).Label(tr("Effort ") + label)
					if row.Clicked() {
						o.Effort = effort
						m.setOptions(agent, o)
						m.Router().Back()
					}
				}
			})
		})
	})
}

type mobilePermissionChoice struct{ value, label, detail string }

func mobilePermissionChoices() []mobilePermissionChoice {
	return []mobilePermissionChoice{
		{"on-request", tr("Ask when needed"), tr("Confirm sensitive actions yourself")},
		{"on-failure", tr("Automatic approval"), tr("Automatic approval handles requests and asks you when needed")},
		{"never", tr("Full access"), tr("Allow the agent to act without asking")},
	}
}

func (m *App) permissionOptionsView(c *ui.Context) {
	agent := m.optionsAgent
	o := m.SendOptions(agent)
	m.settingsHeader(c, "/options/permissions", tr("Permission mode"))
	m.settingsBody(c, func() {
		m.settingsGroup(c, "", "", func() {
			ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(tr("Permission mode")).Children(func() {
				for i, policy := range mobilePermissionChoices() {
					if i > 0 {
						m.settingsSeparator(c.Key("divider:" + policy.value))
					}
					if m.optionChoice(c.Key("policy:"+policy.value), policy.label, policy.detail, policy.value == o.Policy).Clicked() {
						o.Policy = policy.value
						m.setOptions(agent, o)
						m.Router().Back()
					}
				}
			})
		})
	})
}

func (m *App) optionsKey(agent string) string {
	if t := m.Viewer.Current(); m.Open != 0 && t.Contact != nil && model.ThreadAgent(t) == agent {
		return "contact:" + t.Contact.ID
	}
	return agent
}
