package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/widgets"
	"miya/locales"
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

func mobileSelectedModel(a phoneproto.AgentOptions, value string) phoneproto.ModelOption {
	for _, option := range a.Models {
		if option.Value == value {
			return option
		}
	}
	return phoneproto.ModelOption{Value: value}
}

func mobileModelLabel(option phoneproto.ModelOption) string {
	if option.Value != "" {
		return textutil.FirstNonEmpty(option.Label, option.Value)
	}
	// Older desktops may localize the placeholder or prefix before sending it.
	switch option.Label {
	case "Default model", locales.TextIn("zh-Hans", "Default model"):
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(option.Label, "Default · "), locales.TextIn("zh-Hans", "Default · "))
}

func (m *App) optionsView(c *ui.Context) {
	agent := m.optionsAgent
	o := m.SendOptions(agent)
	selected := mobileSelectedModel(m.agentOptions(agent), o.Model)
	m.settingsHeader(c, "/options", tr("Conversation settings"))
	m.settingsBody(c, func() {
		label := textutil.FirstNonEmpty(mobileModelLabel(selected), tr("Not set"))
		effort := textutil.FirstNonEmpty(o.Effort, selected.DefaultEffort)
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
			if m.settingsNavigation(c.Key("effort"), tr("Reasoning effort"), mobileEffortLabel(effort)).Clicked() {
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
		return tr("Not set")
	case "off":
		return tr("Off")
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
	defaultLabel := mobileModelLabel(mobileSelectedModel(a, ""))
	// Keep the current choice checked when the inherited and explicit labels match.
	showInherited := o.Model == "" || mobileModelLabel(mobileSelectedModel(a, o.Model)) != defaultLabel
	m.settingsHeader(c, "/options/model", tr("Model"))
	m.settingsBody(c, func() {
		m.settingsGroup(c, "", "", func() {
			ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(tr("Model")).Children(func() {
				shown := false
				for _, option := range a.Models {
					label := mobileModelLabel(option)
					if defaultLabel != "" && label == defaultLabel && (option.Value == "") != showInherited {
						continue
					}
					label = textutil.FirstNonEmpty(label, tr("Not set"))
					if shown {
						m.settingsSeparator(c.Key("divider:" + option.Value))
					}
					shown = true
					row := m.optionChoice(c.Key("model:"+option.Value), label, "", option.Value == o.Model).Label(tr("Model ") + label)
					if row.Clicked() {
						o.Model, o.Effort = option.Value, option.DefaultEffort
						m.setOptions(agent, o)
						m.Router().Back()
					}
				}
				if !shown {
					ui.Text(c, tr("Connect to your desktop to select a model")).FontSize(m.Font(15)).Padding(16).TextColor(m.Pal.muted).Wrap()
				}
			})
		})
	})
}

func (m *App) effortOptionsView(c *ui.Context) {
	agent := m.optionsAgent
	o := m.SendOptions(agent)
	selected := mobileSelectedModel(m.agentOptions(agent), o.Model)
	defaultEffort := selected.DefaultEffort
	// The inherited effort and its explicit equivalent share a row.
	showInherited := defaultEffort == "" || o.Effort != defaultEffort || !slices.Contains(selected.Efforts, defaultEffort)
	var choices []string
	if showInherited {
		choices = append(choices, "")
	}
	for _, effort := range selected.Efforts {
		if effort != "" && (!showInherited || effort != defaultEffort) {
			choices = append(choices, effort)
		}
	}
	currentEffort := textutil.FirstNonEmpty(o.Effort, defaultEffort)
	m.settingsHeader(c, "/options/effort", tr("Reasoning effort"))
	m.settingsBody(c, func() {
		m.settingsGroup(c, "", "", func() {
			ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(tr("Reasoning effort")).Children(func() {
				for i, effort := range choices {
					if i > 0 {
						m.settingsSeparator(c.Key("divider:" + effort))
					}
					effectiveEffort := textutil.FirstNonEmpty(effort, defaultEffort)
					label := mobileEffortLabel(effectiveEffort)
					row := m.optionChoice(c.Key("effort:"+effort), label, "", effectiveEffort == currentEffort).Label(tr("Effort ") + label)
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
	return agent
}
