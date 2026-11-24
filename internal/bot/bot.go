// Package bot holds the Bots: named assistants working in one project. Each
// has a charter that goes with every message to its conversations
// (docs/personal-agent.md, P4).
package bot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"miya/internal/appdir"
)

// Bot is a name, a charter and the settings of the Bot's conversations.
// Talking to a Bot is opening the conversation the person talks to it in;
// the first message creates it. Each delegated task starts another one.
type Bot struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Note is the Bot's charter in the person's words: what it is for and how
	// it works. It goes with every message to the Bot's conversations, outside
	// the visible conversation, so it survives compaction.
	Note string `json:"note,omitempty"`
	// Agent runs the conversations. Model and Effort are empty for the
	// agent's defaults.
	Agent  string `json:"agent"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Project is the path of the project the Bot lives in: the conversation
	// the person talks to it in runs there, and its memory belongs there.
	Project string `json:"project"`
	// Projects are the other projects the Bot also works in: agents may read
	// and write them, and their conversations may hand the Bot tasks.
	Projects []string `json:"projects,omitempty"`
	// Access is an access level of docs/action-review.md, empty to follow the
	// permission picked in the window.
	Access string `json:"access,omitempty"`
	// Reviewer is the ID of another Bot of the project that reviews, read
	// only, each turn that changes files in the conversation the person talks
	// to this Bot in.
	Reviewer string `json:"reviewer,omitempty"`
	// Session is the conversation the person talks to the Bot in, empty until
	// the agent has named it.
	Session string `json:"session,omitempty"`
	// Others are the sessions of the Bot's other conversations, newest first:
	// the ones the person left for a new one, and the ones delegated tasks
	// started.
	Others []string `json:"others,omitempty"`
	// Reports are what the Bot did on its own and tells the person in its
	// chat, newest first.
	Reports []Report `json:"reports,omitempty"`
	// Preview is the start of the last reply in Session.
	Preview string `json:"preview,omitempty"`
	// Thread is Session's conversation in the running service while its
	// session is not known yet. It is sent to windows and never saved.
	Thread int `json:"thread,omitempty"`
}

// Report is work a Bot finished on its own: a task an agent gave it, a review,
// or a run of an automation given to it. The Bot posts it in its chat, so the
// person sees what it did without looking for the conversation.
type Report struct {
	// Session is the conversation of the work, in Project.
	Session string `json:"session"`
	Project string `json:"project"`
	// Kind is "task" or "automation".
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary,omitempty"`
	NeedsUser bool      `json:"needsUser,omitempty"`
	At        time.Time `json:"at"`
	Read      bool      `json:"read,omitempty"`
}

// Request is a window's change to the Bots: botSave takes Bot, botDelete and
// botReset take ID.
type Request struct {
	ID  string `json:"id,omitempty"`
	Bot Bot    `json:"bot"`
}

const (
	maxName      = 40
	maxNote      = 4000
	maxOthers    = 200
	maxReports   = 20
	summaryRunes = 240
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
	b.Reviewer = strings.TrimSpace(b.Reviewer)
	var mounted []string
	for _, p := range b.Projects {
		p = strings.TrimSpace(p)
		if p == "" || p == b.Project || slices.Contains(mounted, p) {
			continue
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("A Bot's other project must be an absolute path: %q", p)
		}
		mounted = append(mounted, p)
	}
	b.Projects = mounted
	switch {
	case b.Name == "":
		return errors.New("A Bot needs a name")
	case utf8.RuneCountInString(b.Name) > maxName:
		return fmt.Errorf("A Bot's name has at most %d characters", maxName)
	case utf8.RuneCountInString(b.Note) > maxNote:
		return fmt.Errorf("A Bot's charter has at most %d characters", maxNote)
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

// WorksIn reports whether the Bot works in project: its own or another it
// mounts.
func (b Bot) WorksIn(project string) bool {
	return project != "" && (b.Project == project || slices.Contains(b.Projects, project))
}

// AllProjects are the Bot's own project and the others it works in.
func (b Bot) AllProjects() []string {
	return append([]string{b.Project}, b.Projects...)
}

// Owns reports whether session is one of the Bot's conversations.
func (b Bot) Owns(session string) bool {
	return session != "" && (b.Session == session || slices.Contains(b.Others, session))
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
// agents address a Bot by its name. The conversations are the service's to
// record: an edit keeps them, unless the agent or the project changed and
// the old conversations no longer fit.
func (s *Store) Put(b Bot) (Bot, error) {
	if err := b.Normalize(); err != nil {
		return Bot{}, err
	}
	for _, other := range s.Bots {
		if other.ID != b.ID && strings.EqualFold(other.Name, b.Name) {
			return Bot{}, fmt.Errorf("Another Bot is already named %q", other.Name)
		}
	}
	b.Session, b.Others, b.Preview, b.Thread, b.Reports = "", nil, "", 0, nil
	i, ok := s.Find(b.ID)
	if !ok {
		s.Bots = append(s.Bots, b)
		return b, nil
	}
	b.Reports = s.Bots[i].Reports
	if old := s.Bots[i]; old.Agent == b.Agent && old.Project == b.Project {
		b.Session, b.Others, b.Preview, b.Thread = old.Session, old.Others, old.Preview, old.Thread
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

// Reset leaves the conversation the person talks to the Bot in, so the next
// message starts a new one. The old conversation stays the Bot's.
func (s *Store) Reset(id string) bool {
	i, ok := s.Find(id)
	if ok {
		session := s.Bots[i].Session
		s.Bots[i].Session, s.Bots[i].Preview, s.Bots[i].Thread = "", "", 0
		s.AddOther(id, session)
	}
	return ok
}

// SetSession records the conversation the person talks to the Bot in and
// reports a change.
func (s *Store) SetSession(id, session string) bool {
	i, ok := s.Find(id)
	if !ok || s.Bots[i].Session == session {
		return false
	}
	s.Bots[i].Session = session
	s.Bots[i].Others = slices.DeleteFunc(s.Bots[i].Others, func(o string) bool { return o == session })
	return true
}

// AddOther records another conversation of the Bot and reports a change. The
// oldest are forgotten past a limit; they stay ordinary conversations.
func (s *Store) AddOther(id, session string) bool {
	i, ok := s.Find(id)
	if !ok || session == "" || s.Bots[i].Owns(session) {
		return false
	}
	others := append([]string{session}, s.Bots[i].Others...)
	if len(others) > maxOthers {
		others = others[:maxOthers]
	}
	s.Bots[i].Others = others
	return true
}

// Rename follows a conversation of the Bot to the new session its agent moved
// it to, and reports a change.
func (s *Store) Rename(id, old, session string) bool {
	i, ok := s.Find(id)
	if !ok || old == "" || session == "" || old == session {
		return false
	}
	b := &s.Bots[i]
	if b.Session == old {
		b.Session = session
		return true
	}
	if j := slices.Index(b.Others, old); j >= 0 {
		b.Others[j] = session
		return true
	}
	return false
}

// AddReport posts a report in a Bot's chat. A newer report of the same
// conversation replaces the older one; the oldest go past a limit.
func (s *Store) AddReport(id string, r Report) bool {
	i, ok := s.Find(id)
	if !ok || r.Session == "" {
		return false
	}
	kept := []Report{r}
	for _, x := range s.Bots[i].Reports {
		if x.Session != r.Session {
			kept = append(kept, x)
		}
	}
	if len(kept) > maxReports {
		kept = kept[:maxReports]
	}
	s.Bots[i].Reports = kept
	return true
}

// ReadReports marks a Bot's reports read and reports a change.
func (s *Store) ReadReports(id string) bool {
	i, ok := s.Find(id)
	if !ok {
		return false
	}
	changed := false
	for j := range s.Bots[i].Reports {
		if !s.Bots[i].Reports[j].Read {
			s.Bots[i].Reports[j].Read, changed = true, true
		}
	}
	return changed
}

// Unread counts the reports the person has not seen.
func (b Bot) Unread() int {
	n := 0
	for _, r := range b.Reports {
		if !r.Read {
			n++
		}
	}
	return n
}

// Summary is the start of a reply, kept to a few lines for a report.
func Summary(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= summaryRunes {
		return text
	}
	return string([]rune(text)[:summaryRunes]) + "…"
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

// Match finds the Bot one of whose conversations is this session.
func (s *Store) Match(agent, project, session string) (int, bool) {
	if session == "" {
		return 0, false
	}
	for i, b := range s.Bots {
		if b.Agent == agent && b.WorksIn(project) && b.Owns(session) {
			return i, true
		}
	}
	return 0, false
}

// Charter is the note that follows every message to one of the Bot's
// conversations. Agents see it each turn, so it holds after compaction; the
// visible conversation keeps the message as typed.
func Charter(b Bot) string {
	text := fmt.Sprintf("\n\n[Miya] In this conversation you are the Bot named %q. The person and other agents return to you over time; work by this charter.", b.Name)
	if len(b.Projects) > 0 {
		text += "\nYou work in these projects, and may read and change each of them: " + strings.Join(b.AllProjects(), ", ")
	}
	text += "\nKeep what this work teaches: when the person tells you a preference, a decision or a fact about it, save it with the remember tool, scope bot, so that you know it next time."
	if b.Note != "" {
		text += "\nYour charter, in the person's words:\n" + b.Note
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
