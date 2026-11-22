// Package bot holds the Bots: named assistants, each working in one project
// through one standing conversation (docs/personal-agent.md, P4).
package bot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"miya/internal/appdir"
)

// Bot is a name and the settings of one standing conversation. Talking to a
// Bot is opening that conversation; the first message creates it.
type Bot struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Note is the Bot's role in the person's words. It travels once, with the
	// message that starts the standing conversation.
	Note string `json:"note,omitempty"`
	// Agent runs the conversation. Model and Effort are empty for the agent's
	// defaults.
	Agent  string `json:"agent"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Project is the path of the project the Bot works in.
	Project string `json:"project"`
	// Access is an access level of docs/action-review.md, empty to follow the
	// permission picked in the window.
	Access string `json:"access,omitempty"`
	// Session is the standing conversation's session at Agent, empty until
	// the agent has named it.
	Session string `json:"session,omitempty"`
	// Preview is the start of the Bot's last reply.
	Preview string `json:"preview,omitempty"`
	// Thread is the standing conversation's ID in the running service while
	// its session is not known yet. It is sent to windows and never saved.
	Thread int `json:"thread,omitempty"`
}

// Request is a window's change to the Bots: botSave takes Bot, botDelete and
// botReset take ID.
type Request struct {
	ID  string `json:"id,omitempty"`
	Bot Bot    `json:"bot"`
}

const (
	maxName      = 40
	maxNote      = 2000
	previewRunes = 120
)

// Store is the Bots as the service saves them in its data directory.
type Store struct {
	Bots []Bot `json:"bots"`
}

func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Path is where the service keeps the Bots.
func Path() string { return filepath.Join(appdir.Dir(), "bots.json") }

// Normalize trims a Bot, says what it lacks, and gives a new one its ID.
func (b *Bot) Normalize() error {
	b.Name = strings.Join(strings.Fields(b.Name), " ")
	b.Note = strings.TrimSpace(b.Note)
	b.Agent = strings.TrimSpace(b.Agent)
	b.Model, b.Effort = strings.TrimSpace(b.Model), strings.TrimSpace(b.Effort)
	switch {
	case b.Name == "":
		return errors.New("A Bot needs a name")
	case utf8.RuneCountInString(b.Name) > maxName:
		return fmt.Errorf("A Bot's name has at most %d characters", maxName)
	case utf8.RuneCountInString(b.Note) > maxNote:
		return fmt.Errorf("A Bot's role note has at most %d characters", maxNote)
	case b.Agent == "":
		return errors.New("A Bot needs an agent")
	case !filepath.IsAbs(b.Project):
		return errors.New("A Bot needs a project")
	}
	switch b.Access {
	case "", "read", "edit", "external", "full":
	default:
		return fmt.Errorf("Unknown access %q: use read, edit, external or full", b.Access)
	}
	if b.ID == "" {
		b.ID = NewID()
	}
	return nil
}

// Load reads the store. A missing or damaged file is an empty store, and a
// Bot that cannot be used is left out.
func Load(path string) Store {
	var s Store
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	kept := s.Bots[:0]
	for _, b := range s.Bots {
		b.Thread = 0
		if b.Normalize() == nil {
			kept = append(kept, b)
		}
	}
	s.Bots = kept
	return s
}

// Save writes the store through a temporary file, so a crash leaves the old
// one. The IDs of running conversations stay out of it.
func (s Store) Save(path string) error {
	out := Store{Bots: make([]Bot, len(s.Bots))}
	copy(out.Bots, s.Bots)
	for i := range out.Bots {
		out.Bots[i].Thread = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Find(id string) (int, bool) {
	for i, b := range s.Bots {
		if b.ID == id && id != "" {
			return i, true
		}
	}
	return 0, false
}

// Put adds a Bot or replaces the one with its ID. Names are unique, since
// agents address a Bot by its name. The standing conversation is the
// service's to record: an edit keeps it, unless the agent or the project
// changed and the old conversation no longer fits.
func (s *Store) Put(b Bot) (Bot, error) {
	if err := b.Normalize(); err != nil {
		return Bot{}, err
	}
	for _, other := range s.Bots {
		if other.ID != b.ID && strings.EqualFold(other.Name, b.Name) {
			return Bot{}, fmt.Errorf("Another Bot is already named %q", other.Name)
		}
	}
	b.Session, b.Preview, b.Thread = "", "", 0
	i, ok := s.Find(b.ID)
	if !ok {
		s.Bots = append(s.Bots, b)
		return b, nil
	}
	if old := s.Bots[i]; old.Agent == b.Agent && old.Project == b.Project {
		b.Session, b.Preview, b.Thread = old.Session, old.Preview, old.Thread
	}
	s.Bots[i] = b
	return b, nil
}

func (s *Store) Delete(id string) bool {
	i, ok := s.Find(id)
	if ok {
		s.Bots = append(s.Bots[:i], s.Bots[i+1:]...)
	}
	return ok
}

// Reset forgets the standing conversation, so the next message starts a new
// one. The old conversation stays in its project.
func (s *Store) Reset(id string) bool {
	i, ok := s.Find(id)
	if ok {
		s.Bots[i].Session, s.Bots[i].Preview, s.Bots[i].Thread = "", "", 0
	}
	return ok
}

// SetSession records the standing conversation's session and reports a change.
func (s *Store) SetSession(id, session string) bool {
	i, ok := s.Find(id)
	if !ok || s.Bots[i].Session == session {
		return false
	}
	s.Bots[i].Session = session
	return true
}

// SetPreview records the start of the last reply and reports a change.
func (s *Store) SetPreview(id, preview string) bool {
	i, ok := s.Find(id)
	if !ok || s.Bots[i].Preview == preview {
		return false
	}
	s.Bots[i].Preview = preview
	return true
}

// Match finds the Bot whose standing conversation is this session.
func (s *Store) Match(agent, project, session string) (int, bool) {
	if session == "" {
		return 0, false
	}
	for i, b := range s.Bots {
		if b.Session == session && b.Agent == agent && b.Project == project {
			return i, true
		}
	}
	return 0, false
}

// Intro is the note that follows the message starting a Bot's standing
// conversation. It is sent once: later messages go out as typed.
func Intro(b Bot) string {
	text := fmt.Sprintf("\n\n[Miya, sent once] This is the standing conversation of the Bot named %q: the person returns to it over time, and in it you go by that name.", b.Name)
	if b.Note != "" {
		text += "\nYour role, in the person's words:\n" + b.Note
	}
	return text
}

// PreviewOf is one line from the start of a reply, for the Bot's row.
func PreviewOf(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= previewRunes {
		return text
	}
	return string([]rune(text)[:previewRunes]) + "…"
}
