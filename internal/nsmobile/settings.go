package nsmobile

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/nsgo-dev/nsgo/core"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"miya/internal/prefs"
	"miya/internal/textutil"
)

// settings is the old phone app's grouped settings list.
func (m *App) settings(cx *core.Scope) core.Node {
	m.refreshDesktopAuthentication()
	unread := live(m, func() int { return len(m.State.Notifications.Unread) })
	return m.groupedPage(cx,
		core.Show{When: live(m, func() bool { return m.appUpdate.url != "" }), Then: func(cx *core.Scope) core.Node {
			return m.group(cx, "",
				nil, gRow{ID: "mobile-update", Label: fmt.Sprintf(tr("Available version: %s"), m.appUpdate.version),
					Action: true, Press: func() { cx.OpenURL(m.appUpdate.url, nil) }})
		}},
		m.group(cx, tr("General"), nil,
			gRow{ID: "language", Label: tr("Interface language"), Value: func() string { return languageName(currentLanguage()) }, Chevron: true, Press: func() { m.pages.Open("language") }},
			gRow{ID: "voice-settings", Label: tr("Voice input"), Chevron: true, Press: func() { m.pages.Open("voice") }},
		),
		m.group(cx, tr("Desktop connections"), groupedFooter(cx, "", live(m, m.ConnectionLine)),
			gRow{ID: "manage-desktops", Label: tr("Manage desktops"), Value: live(m, func() string {
				return textutil.FirstNonEmpty(strings.TrimSuffix(m.DesktopName(), ".local"), tr("Desktop"))
			}), Chevron: true, Press: func() { m.pages.Open("desktops") }},
			gRow{ID: "reconnect", Label: tr("Reconnect"), Action: true, Disabled: live(m, func() bool { return m.Link == "" }), Press: func() { m.Connect(m.Link); m.pages.Open("") }},
			gRow{ID: "add-desktop", Label: tr("Add desktop"), Action: true, Press: func() { m.pages.Open("pair") }},
		),
		m.group(cx, tr("Conversations"), nil,
			gRow{ID: "message-options", Label: tr("Model and permissions"), Chevron: true, Press: func() { m.optionsAgent = m.Agent(); m.pages.Open("options") }},
			gRow{ID: "memory-settings", Label: tr("Shared memory"), Chevron: true, Press: func() { m.pages.Open("memory") }},
		),
		m.group(cx, tr("Notifications"), groupedFooter(cx, "", func() string { return tr("Get alerts when tasks finish or need confirmation") }),
			gRow{ID: "push-settings", Label: tr("Mobile notifications"), Chevron: true, Press: func() { m.pages.Open("push") }},
			gRow{ID: "read-all", Label: tr("Mark all as read"), Action: true, Value: func() string { return strconv.Itoa(unread()) },
				Disabled: live(m, func() bool { return !m.Connected || len(m.State.Notifications.Unread) == 0 }), Press: m.MarkAllRead},
		),
		m.group(cx, "", nil,
			gRow{Label: tr("Device authentication"), ValueID: "desktop-authentication-status", Value: live(m, m.desktopAuthenticationLabel)},
		),
		m.group(cx, "", groupedFooter(cx, "settings-error", live(m, func() string { return strings.TrimSpace(m.Error + "\n" + m.PersistenceError) })),
			gRow{ID: "disconnect", Label: tr("Disconnect"), Destructive: true, Press: func() { m.CloseDesktopPage(false); m.Disconnect(); m.pages.Open("") }},
			gRow{ID: "forget", Label: tr("Forget desktop"), Destructive: true, Press: func() { m.Forget(); m.pages.Open("") }},
		),
	)
}

func currentLanguage() string {
	return prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage"))
}

func languageName(tag string) string {
	switch tag {
	case "zh-Hans":
		return display.Self.Name(language.SimplifiedChinese)
	case "en":
		return display.Self.Name(language.English)
	}
	return tr("Follow system")
}

func (m *App) languageSettings(cx *core.Scope) core.Node {
	current := currentLanguage()
	var rows []gRow
	for _, tag := range []string{"", "zh-Hans", "en"} {
		rows = append(rows, gRow{ID: "language-" + tag, Label: languageName(tag), Checked: func() bool { return tag == current }, Press: func() {
			prefs.Save("interfaceLanguage", tag)
			cx.SetLocale(tag)
			m.pages.Open("settings")
		}})
	}
	return m.groupedPage(cx, m.group(cx, "", nil, rows...))
}

func policyChoices() []struct{ value, label, detail string } {
	return []struct{ value, label, detail string }{
		{"on-request", tr("Ask when needed"), tr("Confirm sensitive actions yourself")},
		{"on-failure", tr("Automatic approval"), tr("Automatic approval handles requests and asks you when needed")},
		{"never", tr("Full access"), tr("Allow the agent to act without asking")},
	}
}

// options are the conversation settings and their choice pages: grouped
// rows, the chosen one checked, as the old phone app's.
func (m *App) options(cx *core.Scope, page string) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		agent := m.optionsAgent
		if agent == "" {
			agent = m.Agent()
		}
		opts := m.SendOptions(agent)
		available := m.AgentOptions(agent)
		text := func(s string) func() string { return func() string { return s } }
		choice := func(id, label, detail string, selected bool, press func()) gRow {
			return gRow{ID: id, Label: label, Detail: detail, Checked: func() bool { return selected }, Press: press}
		}
		var groups []core.Node
		switch page {
		case "options":
			model := textutil.FirstNonEmpty(opts.Model, tr("Model default"))
			for _, v := range available.Models {
				if v.Value == opts.Model {
					model = textutil.FirstNonEmpty(v.Label, model)
					break
				}
			}
			policy := opts.Policy
			for _, p := range policyChoices() {
				if p.value == opts.Policy {
					policy = p.label
				}
			}
			groups = append(groups,
				m.group(cx, m.agentName(agent), groupedFooter(cx, "", text(tr("Applies to new messages sent from your phone. Desktop defaults are unchanged."))),
					gRow{ID: "pick-agent", Label: tr("Agent"), Value: text(m.agentName(agent)), Chevron: true, Disabled: live(m, func() bool { return m.Open != 0 }), Press: func() { m.pages.Open("agent") }},
					gRow{ID: "pick-model", Label: tr("Model"), Value: text(model), Chevron: true, Press: func() { m.pages.Open("model") }},
					gRow{ID: "pick-effort", Label: tr("Reasoning effort"), Value: text(effortLabel(opts.Effort)), Chevron: true, Press: func() { m.pages.Open("effort") }},
					gRow{ID: "pick-policy", Label: tr("Permission mode"), Value: text(policy), Chevron: true, Press: func() { m.pages.Open("policy") }},
				),
			)
			if _, customized := m.Options[agent]; customized {
				groups = append(groups, m.group(cx, "", nil,
					gRow{ID: "reset-options", Label: tr("Use desktop defaults"), Action: true, Press: func() { delete(m.Options, agent); m.SaveOptions() }}))
			}
		case "agent":
			var rows []gRow
			for _, d := range m.AgentDescriptors() {
				rows = append(rows, choice("agent-"+d.ID, d.Name, "", d.ID == agent, func() { m.NewAgent = d.ID; m.optionsAgent = d.ID; m.pages.Open("options") }))
			}
			groups = append(groups, m.group(cx, "", nil, rows...))
		case "model":
			var rows []gRow
			for _, v := range available.Models {
				rows = append(rows, choice("model-"+v.Value, v.Label, "", v.Value == opts.Model, func() {
					opts.Model, opts.Effort = v.Value, v.DefaultEffort
					m.SetOptions(agent, opts)
					m.pages.Open("options")
				}))
			}
			if len(rows) == 0 {
				groups = append(groups, groupedState(cx, "settings", text(tr("Connect to your desktop to select a model")), nil, nil))
			} else {
				groups = append(groups, m.group(cx, "", nil, rows...))
			}
		case "effort":
			efforts := []string{""}
			for _, v := range available.Models {
				if v.Value == opts.Model {
					for _, e := range v.Efforts {
						if e != "" {
							efforts = append(efforts, e)
						}
					}
					break
				}
			}
			var rows []gRow
			for i, effort := range efforts {
				rows = append(rows, choice("effort-"+strconv.Itoa(i), effortLabel(effort), "", effort == opts.Effort, func() { opts.Effort = effort; m.SetOptions(agent, opts); m.pages.Open("options") }))
			}
			groups = append(groups, m.group(cx, "", nil, rows...))
		case "policy":
			var rows []gRow
			for _, p := range policyChoices() {
				rows = append(rows, choice("policy-"+p.value, p.label, p.detail, p.value == opts.Policy, func() { opts.Policy = p.value; m.SetOptions(agent, opts); m.pages.Open("options") }))
			}
			groups = append(groups, m.group(cx, "", nil, rows...))
		}
		return m.groupedPage(cx, groups...)
	})
}
func effortLabel(effort string) string {
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
