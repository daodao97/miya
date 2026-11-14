package nsmobile

import (
	"strconv"
	"time"
	"unicode"

	"github.com/nsgo-dev/nsgo/core"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"miya/internal/prefs"
)

func (m *App) settings(cx *core.Scope) core.Node {
	return scroll(text(tr("Settings")),
		m.button("memory-settings", tr("Shared memory"), nil, func() { m.sheet.Set("memory") }),
		m.button("language", tr("Interface language"), nil, func() { m.sheet.Set("language") }),
		m.button("reconnect", tr("Reconnect"), live(m, func() bool { return m.Link == "" }), func() { m.Connect(m.Link); m.sheet.Set("") }),
		m.button("add-desktop", tr("Add desktop"), nil, func() { m.sheet.Set("pair") }),
		m.button("message-options", tr("Model and permissions"), nil, func() { m.optionsAgent = m.Agent(); m.sheet.Set("options") }),
		m.button("read-all", tr("Mark all as read"), live(m, func() bool { return !m.Connected || len(m.State.Notifications.Unread) == 0 }), m.MarkAllRead),
		m.button("disconnect", tr("Disconnect"), nil, func() { m.Disconnect(); m.sheet.Set("") }),
		m.button("forget", tr("Forget desktop"), nil, func() { m.Forget(); m.sheet.Set("") }),
		core.Text{ContentFunc: live(m, func() string { return m.Error + "\n" + m.PersistenceError })},
		// TODO(settings): voice and push controls belong to their deferred adapters.
	)
}
func (m *App) languageSettings(cx *core.Scope) core.Node {
	var nodes = []core.Node{text(tr("Interface language"))}
	for _, choice := range []struct{ tag, label string }{{"", tr("Follow system")}, {"zh-Hans", display.Self.Name(language.SimplifiedChinese)}, {"en", display.Self.Name(language.English)}} {
		nodes = append(nodes, m.button("language-"+choice.tag, choice.label, nil, func() { prefs.Save("interfaceLanguage", choice.tag); cx.SetLocale(choice.tag); m.sheet.Set("settings") }))
	}
	return scroll(nodes...)
}
func (m *App) options(cx *core.Scope, page string) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		m.revision.Get()
		agent := m.optionsAgent
		if agent == "" {
			agent = m.Agent()
		}
		opts := m.SendOptions(agent)
		available := m.AgentOptions(agent)
		nodes := []core.Node{text(tr("Conversation settings"))}
		choice := func(id, label string, selected bool, press func()) core.Node {
			if selected {
				label = "✓ " + label
			}
			return m.button(id, label, nil, press)
		}
		switch page {
		case "options":
			nodes = append(nodes, text(tr("Applies to new messages sent from your phone. Desktop defaults are unchanged.")),
				m.button("pick-agent", tr("Agent"), live(m, func() bool { return m.Open != 0 }), func() { m.sheet.Set("agent") }),
				m.button("pick-model", tr("Model")+": "+opts.Model, nil, func() { m.sheet.Set("model") }),
				m.button("pick-effort", tr("Reasoning effort")+": "+effortLabel(opts.Effort), nil, func() { m.sheet.Set("effort") }),
				m.button("pick-policy", tr("Permission mode"), nil, func() { m.sheet.Set("policy") }),
				m.button("reset-options", tr("Use desktop defaults"), nil, func() { delete(m.Options, agent); m.SaveOptions() }))
		case "agent":
			for _, d := range m.AgentDescriptors() {
				nodes = append(nodes, choice("agent-"+d.ID, d.Name, d.ID == agent, func() { m.NewAgent = d.ID; m.optionsAgent = d.ID; m.sheet.Set("options") }))
			}
		case "model":
			if len(available.Models) == 0 {
				nodes = append(nodes, text(tr("Connect to your desktop to select a model")))
			}
			for _, model := range available.Models {
				nodes = append(nodes, choice("model-"+model.Value, model.Label, model.Value == opts.Model, func() {
					opts.Model, opts.Effort = model.Value, model.DefaultEffort
					m.SetOptions(agent, opts)
					m.sheet.Set("options")
				}))
			}
		case "effort":
			efforts := []string{""}
			for _, model := range available.Models {
				if model.Value == opts.Model {
					for _, v := range model.Efforts {
						if v != "" {
							efforts = append(efforts, v)
						}
					}
					break
				}
			}
			for i, effort := range efforts {
				nodes = append(nodes, choice("effort-"+strconv.Itoa(i), effortLabel(effort), effort == opts.Effort, func() { opts.Effort = effort; m.SetOptions(agent, opts); m.sheet.Set("options") }))
			}
		case "policy":
			for _, policy := range []struct{ value, label, detail string }{{"on-request", tr("Ask when needed"), tr("Confirm sensitive actions yourself")}, {"on-failure", tr("Automatic approval"), tr("Automatic approval handles requests and asks you when needed")}, {"never", tr("Full access"), tr("Allow the agent to act without asking")}} {
				nodes = append(nodes, choice("policy-"+policy.value, policy.label+"\n"+policy.detail, policy.value == opts.Policy, func() { opts.Policy = policy.value; m.SetOptions(agent, opts); m.sheet.Set("options") }))
			}
		}
		nodes = append(nodes, m.button("options-close", tr("Done"), nil, func() { m.sheet.Set("") }))
		return scroll(nodes...)
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
func quotaResetLabel(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).Local()
	days := [...]string{tr("Sun"), tr("Mon"), tr("Tue"), tr("Wed"), tr("Thu"), tr("Fri"), tr("Sat")}
	return days[t.Weekday()] + t.Format(" 15:04")
}
