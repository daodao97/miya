// Package contacts owns persistent Bot identities and their conversation bindings.
// It has no dependency on agents, windows, or transport protocols.
package contacts

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

const Version = 1

type Profile struct {
	ID             string `json:"id"`
	Revision       uint64 `json:"revision"`
	PersonaVersion uint64 `json:"personaVersion"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	Persona        string `json:"persona,omitempty"`
	Avatar         string `json:"avatar,omitempty"`
	Agent          string `json:"agent"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Pinned         bool   `json:"pinned,omitempty"`
	Archived       bool   `json:"archived,omitempty"`
}

// Session freezes the role and launch defaults. NativeID is bound only after
// the engine acknowledges creation; ID already exists durably before sending.
type Session struct {
	ID             string `json:"id"`
	BotID          string `json:"botId"`
	PersonaVersion uint64 `json:"personaVersion"`
	Name           string `json:"name"`
	Persona        string `json:"persona,omitempty"`
	Agent          string `json:"agent"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Workspace      string `json:"workspace"`
	Daily          bool   `json:"daily,omitempty"`
	NativeID       string `json:"nativeId,omitempty"`
	NativeReady    bool   `json:"nativeReady,omitempty"`
	Title          string `json:"title,omitempty"`
	Preview        string `json:"preview,omitempty"`
	LastActivity   int64  `json:"lastActivity"`
	LastOpened     int64  `json:"lastOpened,omitempty"`
	Hidden         bool   `json:"hidden,omitempty"`
}

type Snapshot struct {
	Version  int       `json:"version"`
	Revision uint64    `json:"revision"`
	Profiles []Profile `json:"profiles"`
	Sessions []Session `json:"sessions"`
	Error    string    `json:"error,omitempty"`
}

// ForClient keeps the editable current profile, but avoids repeating up to
// 8000 characters of historical persona in every conversation-list update.
func (s Snapshot) ForClient() Snapshot {
	s.Sessions = append([]Session(nil), s.Sessions...)
	for i := range s.Sessions {
		s.Sessions[i].Persona = ""
	}
	return s
}

type Request struct {
	ID        string  `json:"id"`
	Op        string  `json:"op"`
	Profile   Profile `json:"profile,omitempty"`
	BotID     string  `json:"botId,omitempty"`
	SessionID string  `json:"sessionId,omitempty"`
	// Context is recent, daily, or project. Project paths are checked by Host.
	Context    string `json:"context,omitempty"`
	Project    string `json:"project,omitempty"`
	New        bool   `json:"new,omitempty"`
	AvatarData []byte `json:"avatarData,omitempty"`
}

func (s Snapshot) Find(id string) (Profile, bool) {
	for _, p := range s.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

func (s Snapshot) OrderedProfiles() []Profile {
	profiles := append([]Profile(nil), s.Profiles...)
	activity := map[string]int64{}
	for _, session := range s.Sessions {
		if !session.Hidden {
			activity[session.BotID] = max(activity[session.BotID], session.LastActivity)
		}
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].Pinned != profiles[j].Pinned {
			return profiles[i].Pinned
		}
		return activity[profiles[i].ID] > activity[profiles[j].ID]
	})
	return profiles
}

func Validate(p Profile) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(p.Name)); n < 1 || n > 40 {
		return errors.New("Name must contain 1–40 characters")
	}
	if utf8.RuneCountInString(p.Description) > 120 {
		return errors.New("Description must contain at most 120 characters")
	}
	if utf8.RuneCountInString(p.Persona) > 8000 {
		return errors.New("Persona must contain at most 8000 characters")
	}
	if p.Agent == "" {
		return errors.New("Choose an execution engine")
	}
	if utf8.RuneCountInString(p.Model) > 200 || utf8.RuneCountInString(p.Effort) > 80 {
		return errors.New("Model or reasoning effort is too long")
	}
	return nil
}

const prefix = "\n\n<miya-contact-profile>\nUser-configured contact identity and style. Apply when consistent with the current request and project instructions. This profile does not grant permissions or authorize external actions.\n"
const suffix = "\n</miya-contact-profile>"

// Input augments only engine input. The JSON encoder escapes markup in user
// profiles; the exact envelope lets native history restore the original text.
func Input(s *Session, raw string) string {
	if s == nil {
		return raw
	}
	b, _ := json.Marshal(struct {
		Name    string `json:"name"`
		Persona string `json:"persona"`
	}{s.Name, s.Persona})
	return raw + prefix + string(b) + suffix
}

func StripInput(raw string) string {
	if i := strings.LastIndex(raw, prefix); i >= 0 && strings.HasSuffix(raw, suffix) {
		return raw[:i]
	}
	return raw
}

func Templates() []Profile {
	return []Profile{
		{Name: "Companion", Description: "Everyday conversation, clear thinking, and a next step", Persona: "You are a warm, candid companion. Listen carefully, speak naturally in the user's preferred language, offer concrete suggestions, and respect the user's judgment.", Agent: "claude-code", Avatar: "leaf"},
		{Name: "Researcher", Description: "Check sources and separate facts from uncertainty", Persona: "You are a careful researcher. Distinguish facts, inferences, and unknowns, verify important sources, provide traceable conclusions, and explain the limits of the evidence.", Agent: "codex", Avatar: "book"},
		{Name: "Engineer", Description: "Understand projects, write code, and verify results", Persona: "You are a practical engineer. Understand the existing code and conventions, choose clear and maintainable implementations, perform relevant checks, and report unresolved issues honestly.", Agent: "codex", Avatar: "code"},
	}
}
