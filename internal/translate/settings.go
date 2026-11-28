package translate

import agentmeta "miya/internal/agent/catalog"

// Settings are how replies translate, kept where the agents run and shared
// by the desktop and its phones: whether replies offer it, automatically or
// not, into which language, by which agent and model.
type Settings struct {
	// Off hides the translate button; translation is on by default.
	Off bool `json:"off,omitempty"`
	// Auto translates replies as they show, without a click.
	Auto bool `json:"auto,omitempty"`
	// Target is the language, a Targets tag; DefaultTarget when empty.
	Target string `json:"target,omitempty"`
	// Agent translates, the conversation's when empty; Model is its model,
	// its default when empty, and only ever set with Agent.
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
}

// The keys Settings are kept under, as preferences.
const (
	offPreference    = "reply-translation"
	autoPreference   = "reply-translation-auto"
	targetPreference = "reply-translation-target"
	agentPreference  = "reply-translation-agent"
	modelPreference  = "reply-translation-model"
)

// Normalize returns s with a known target and agent, and a model only
// with an agent.
func (s Settings) Normalize() Settings {
	s.Target = TargetFor(s.Target).Tag
	if _, ok := agentmeta.Lookup(s.Agent); !ok {
		s.Agent = ""
	}
	if s.Agent == "" {
		s.Model = ""
	}
	return s
}

// LoadSettings reads Settings with load, a preference's value by key.
func LoadSettings(load func(key string) string) Settings {
	return Settings{
		Off:    load(offPreference) == "off",
		Auto:   load(autoPreference) == "on",
		Target: load(targetPreference),
		Agent:  load(agentPreference),
		Model:  load(modelPreference),
	}.Normalize()
}

// Save writes s with save, a preference's value by key.
func (s Settings) Save(save func(key, value string)) {
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	save(offPreference, onOff(!s.Off))
	save(autoPreference, onOff(s.Auto))
	save(targetPreference, s.Target)
	save(agentPreference, s.Agent)
	save(modelPreference, s.Model)
}
