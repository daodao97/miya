package mobile

import (
	"encoding/json"
	"errors"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/prefs"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/translate"
	"miya/internal/widgets"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// translateReply asks the desktop's agent for a reply of t in the language
// lang; done gets it on the main thread.
func (m *App) translateReply(t model.Thread, text, lang string, done func(string, error)) {
	if m.Conn == nil || !m.Connected || !m.State.Translate {
		done("", errors.New(tr("Not connected to desktop")))
		return
	}
	if m.TranslateCalls == nil {
		m.TranslateCalls = map[string]func(string, error){}
	}
	id := textutil.NewID()
	m.TranslateCalls[id] = done
	choice := widgets.Translation()
	r := remote.Request{Op: "translate", RequestID: id, Text: text, Lang: lang, Agent: textutil.FirstNonEmpty(choice.Agent, model.ThreadAgent(t)),
		Model: choice.Model, Project: t.Project}
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() { m.finishTranslation(id, "", err) })
		}
	}()
}

// lookupTranslations asks the desktop for the translations it keeps of
// keys; done gets those found on the main thread.
func (m *App) lookupTranslations(keys []string, done func(map[string]string)) bool {
	if m.Conn == nil || !m.Connected || !m.State.Translate {
		return false
	}
	if m.LookupCalls == nil {
		m.LookupCalls = map[string]func(map[string]string){}
	}
	id := textutil.NewID()
	m.LookupCalls[id] = done
	r := remote.Request{Op: "translation-lookup", RequestID: id, Keys: keys}
	conn, gen := m.Conn, m.Generation
	go func() {
		if conn.Send(r) != nil {
			m.onMain(gen, func() { delete(m.LookupCalls, id) })
		}
	}()
	return true
}

// ReceiveTranslationLookup takes the desktop's answer to a
// translation-lookup request.
func (m *App) ReceiveTranslationLookup(raw json.RawMessage) {
	var result phoneproto.TranslationLookupResult
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	done := m.LookupCalls[result.RequestID]
	delete(m.LookupCalls, result.RequestID)
	if done != nil && len(result.Translations) > 0 {
		done(result.Translations)
	}
}

// saveTranslationSettings keeps a change of how replies translate on the
// desktop, which keeps them for its windows and phones; a desktop too old
// to keep them leaves them to this phone.
func (m *App) saveTranslationSettings(s translate.Settings) {
	if m.Conn == nil || !m.Connected || m.State.Translation == nil {
		s.Save(prefs.Save)
		return
	}
	raw, _ := json.Marshal(s)
	m.State.Translation = &s
	conn := m.Conn
	go conn.Send(remote.Request{Op: "translation-settings", Translation: raw})
}

// ReceiveTranslation takes the desktop's answer to a translate request.
func (m *App) ReceiveTranslation(raw json.RawMessage, message string) {
	var result phoneproto.TranslateResult
	if json.Unmarshal(raw, &result) != nil || result.RequestID == "" {
		return
	}
	var err error
	if message != "" {
		err = errors.New(message)
	}
	m.finishTranslation(result.RequestID, result.Text, err)
}

func (m *App) finishTranslation(id, text string, err error) {
	done := m.TranslateCalls[id]
	if done == nil {
		return
	}
	delete(m.TranslateCalls, id)
	done(text, err)
}

// interruptTranslations ends the translations a lost connection will not
// answer.
func (m *App) interruptTranslations() {
	calls := m.TranslateCalls
	m.TranslateCalls, m.LookupCalls = nil, nil
	for _, done := range calls {
		done("", errors.New(tr("Disconnected. Reconnect to try again.")))
	}
}

// translationSettingValue sums up the device's choice in the settings list.
func translationSettingValue() string {
	if !widgets.TranslationEnabled() {
		return tr("Off")
	}
	return i18n.Autonym(widgets.TranslationTarget().Tag)
}

func (m *App) translationSettingsPage(c *ui.Context) {
	m.settingsHeader(c, "/settings/translation", tr("Reply translation"))
	m.settingsBody(c, func() {
		choice := widgets.Translation()
		m.settingsGroup(c.Key("enabled"), "", widgets.TranslationFootnote(), func() {
			ui.Row(c).FillWidth().MinHeight(52).Padding(4, 16).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Translate replies")).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
				on := choice.On
				if m.PhoneSwitch(c.Key("toggle"), &on, tr("Translate replies")).Changed() {
					widgets.SetTranslationEnabled(on)
				}
			})
		})
		if !choice.On {
			return
		}
		m.settingsGroup(c.Key("auto"), "", tr("Translates replies in another language as they show, without a click."), func() {
			ui.Row(c).FillWidth().MinHeight(52).Padding(4, 16).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Translate automatically")).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
				on := choice.Auto
				if m.PhoneSwitch(c.Key("toggle"), &on, tr("Translate automatically")).Changed() {
					widgets.SetTranslationAuto(on)
				}
			})
		})
		targets := make([]translationOption, len(translate.Targets))
		for i, t := range translate.Targets {
			targets[i] = translationOption{t.Tag, i18n.Autonym(t.Tag)}
		}
		m.translationChoices(c.Key("target"), tr("Translate into"), targets, choice.Target.Tag, widgets.SetTranslationTarget)
		agents := make([]translationOption, len(widgets.TranslationAgents()))
		for i, a := range widgets.TranslationAgents() {
			agents[i] = translationOption{a, widgets.TranslationAgentName(a)}
		}
		m.translationChoices(c.Key("agent"), tr("Translating agent"), agents, choice.Agent, widgets.SetTranslationAgent)
		if choice.Agent == "" {
			return
		}
		models := []translationOption{{"", tr("Default model")}}
		for _, o := range m.agentOptions(choice.Agent).Models {
			if o.Value != "" {
				models = append(models, translationOption{o.Value, textutil.FirstNonEmpty(o.Label, o.Value)})
			}
		}
		if choice.Model != "" && !hasTranslationOption(models, choice.Model) {
			// Chosen on another desktop, or no longer listed: still shown.
			models = append(models, translationOption{choice.Model, choice.Model})
		}
		m.translationChoices(c.Key("model-"+choice.Agent), tr("Translating model"), models, choice.Model, widgets.SetTranslationModel)
	})
}

type translationOption struct{ value, label string }

func hasTranslationOption(options []translationOption, value string) bool {
	for _, o := range options {
		if o.value == value {
			return true
		}
	}
	return false
}

// translationChoices is a group of options of which one is chosen.
func (m *App) translationChoices(c *ui.Context, title string, options []translationOption, selected string, pick func(string)) {
	m.settingsGroup(c, title, "", func() {
		ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(title).Children(func() {
			for i, o := range options {
				if i > 0 {
					m.settingsSeparator(c)
				}
				row := m.settingsButton(c.Key(o.value), o.label).Gap(12).Role(ui.RoleRadio).Checked(selected == o.value)
				row.Children(func() {
					ui.Text(c, o.label).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
					if selected == o.value {
						widgets.Icon(c, "check", 18, m.Pal.link)
					}
				})
				if row.Clicked() {
					pick(o.value)
				}
			}
		})
	})
}
