package widgets

import (
	"sync"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/prefs"
	"miya/internal/translate"
)

// TranslationAgents are the agents that can translate, every agent of
// Miya's, after "", the agent of the conversation.
func TranslationAgents() []string { return append([]string{""}, agentmeta.IDs()...) }

// TranslationChoice is how replies translate, as the view reads it.
type TranslationChoice struct {
	On bool
	// Auto translates the replies that show, without a click.
	Auto   bool
	Target translate.Target
	// Agent translates, the conversation's when ""; Model is its model,
	// its default when "", and only ever set with Agent.
	Agent, Model string
}

// translation holds the settings of reply translation. They are kept
// where the agents run, the desktop's background service, and shared by
// its windows and phones: the host gives them as the service's state
// comes (SetTranslationSettings), and SaveTranslationSettings sends a
// change there. Until then, and with no service, they are this process's
// preferences.
var translation struct {
	once     sync.Once
	mu       sync.Mutex
	settings translate.Settings
}

// SaveTranslationSettings, when set, keeps a change of the settings where
// the agents run; without it the preferences of this process keep it.
var SaveTranslationSettings func(translate.Settings)

func translationSettings() translate.Settings {
	translation.once.Do(func() { translation.settings = translate.LoadSettings(prefs.Load) })
	translation.mu.Lock()
	defer translation.mu.Unlock()
	return translation.settings
}

// SetTranslationSettings takes the settings the service keeps.
func SetTranslationSettings(s translate.Settings) {
	translationSettings()
	translation.mu.Lock()
	translation.settings = s.Normalize()
	translation.mu.Unlock()
}

// Translation returns how replies translate.
func Translation() TranslationChoice {
	s := translationSettings()
	return TranslationChoice{On: !s.Off, Auto: s.Auto, Target: translate.TargetFor(s.Target), Agent: s.Agent, Model: s.Model}
}

func setTranslation(f func(*translate.Settings)) {
	s := translationSettings()
	f(&s)
	s = s.Normalize()
	SetTranslationSettings(s)
	if save := SaveTranslationSettings; save != nil {
		save(s)
		return
	}
	s.Save(prefs.Save)
}

// TranslationEnabled reports whether replies offer to translate themselves.
func TranslationEnabled() bool { return Translation().On }

// SetTranslationEnabled turns the offer on or off.
func SetTranslationEnabled(on bool) { setTranslation(func(s *translate.Settings) { s.Off = !on }) }

// SetTranslationAuto turns automatic translation on or off.
func SetTranslationAuto(on bool) { setTranslation(func(s *translate.Settings) { s.Auto = on }) }

// TranslationTarget is the language replies are translated into.
func TranslationTarget() translate.Target { return Translation().Target }

// SetTranslationTarget chooses the language replies are translated into.
func SetTranslationTarget(tag string) { setTranslation(func(s *translate.Settings) { s.Target = tag }) }

// SetTranslationAgent chooses the agent that translates, "" for the
// conversation's; its model goes back to the agent's default.
func SetTranslationAgent(agent string) {
	setTranslation(func(s *translate.Settings) {
		if agent != s.Agent {
			s.Agent, s.Model = agent, ""
		}
	})
}

// SetTranslationModel chooses the model of the chosen agent, "" for its
// default.
func SetTranslationModel(model string) {
	setTranslation(func(s *translate.Settings) { s.Model = model })
}

// TranslationAgentName names an agent of TranslationAgents.
func TranslationAgentName(agent string) string {
	if agent == "" {
		return tr("Conversation's agent")
	}
	return agentmeta.For(agent).Name
}

// TranslationModel is a model an agent offers for translating.
type TranslationModel struct{ Value, Label string }

// TranslationModelName names the chosen model among models.
func TranslationModelName(model string, models []TranslationModel) string {
	if model == "" {
		return tr("Default model")
	}
	for _, m := range models {
		if m.Value == model {
			return m.Label
		}
	}
	return model
}

// TranslationFootnote explains what a translation costs.
func TranslationFootnote() string {
	return tr("Replies in another language get a translate button. The agent translates them on your computer, using its quota. These settings are your computer's, shared by its windows and phones.")
}

// TranslationSettings is the desktop's group for reply translation; models
// lists an agent's models.
func TranslationSettings(c *ui.Context, models func(agent string) []TranslationModel) {
	choice := Translation()
	rows := []func(){func() {
		SettingRow(c, tr("Translate replies"), "", func() {
			on := choice.On
			ui.Switch(c.Key("translation-enabled"), &on).Label(tr("Translate replies"))
			if on != choice.On {
				SetTranslationEnabled(on)
			}
		})
	}}
	if choice.On {
		rows = append(rows, func() {
			SettingRow(c, tr("Translate automatically"), tr("Translates replies in another language as they show, without a click."), func() {
				on := choice.Auto
				ui.Switch(c.Key("translation-auto"), &on).Label(tr("Translate automatically"))
				if on != choice.Auto {
					SetTranslationAuto(on)
				}
			})
		}, func() {
			SettingRow(c, tr("Translate into"), "", func() {
				labels := make([]string, len(translate.Targets))
				for i, t := range translate.Targets {
					labels[i] = i18n.Autonym(t.Tag)
				}
				selected := i18n.Autonym(choice.Target.Tag)
				if ui.Select(c.Key("translation-target"), &selected, labels).Label(tr("Translate into")).Changed() {
					for _, t := range translate.Targets {
						if i18n.Autonym(t.Tag) == selected {
							SetTranslationTarget(t.Tag)
						}
					}
				}
			})
		}, func() {
			SettingRow(c, tr("Translating agent"), "", func() {
				agents := TranslationAgents()
				labels := make([]string, len(agents))
				for i, a := range agents {
					labels[i] = TranslationAgentName(a)
				}
				selected := TranslationAgentName(choice.Agent)
				if ui.Select(c.Key("translation-agent"), &selected, labels).Label(tr("Translating agent")).Changed() {
					for _, a := range agents {
						if TranslationAgentName(a) == selected {
							SetTranslationAgent(a)
						}
					}
				}
			})
		})
		if choice.Agent != "" {
			rows = append(rows, func() {
				SettingRow(c, tr("Translating model"), "", func() {
					list := append([]TranslationModel{{"", tr("Default model")}}, models(choice.Agent)...)
					listed := false
					for _, m := range list {
						listed = listed || m.Value == choice.Model
					}
					if !listed {
						// Chosen on another desktop, or no longer listed: still shown.
						list = append(list, TranslationModel{choice.Model, choice.Model})
					}
					labels := make([]string, len(list))
					for i, m := range list {
						labels[i] = m.Label
					}
					selected := TranslationModelName(choice.Model, list)
					if ui.Select(c.Key("translation-model-"+choice.Agent), &selected, labels).Label(tr("Translating model")).Changed() {
						for _, m := range list {
							if m.Label == selected {
								SetTranslationModel(m.Value)
							}
						}
					}
				})
			})
		}
	}
	SettingsGroup(c, tr("Reply translation"), TranslationFootnote(), rows...)
}
