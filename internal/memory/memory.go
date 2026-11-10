// Package memory owns Miya's local, versioned memories. Only the background
// service constructs a Store; adapters supply verified caller identities.
package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const FormatVersion = 1

// Scope uses the canonical absolute workspace, not a repository name or remote.
type Scope struct {
	Kind        string `json:"kind"`
	ProjectKey  string `json:"projectKey,omitempty"`
	ProjectPath string `json:"projectPath,omitempty"`
}

func UserScope() Scope { return Scope{Kind: "user"} }
func ProjectScope(path string) (Scope, error) {
	if !filepath.IsAbs(path) {
		return Scope{}, errors.New("Project path must be absolute")
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	} else if !os.IsNotExist(err) {
		return Scope{}, err
	}
	sum := sha256.Sum256([]byte(path))
	return Scope{Kind: "project", ProjectKey: hex.EncodeToString(sum[:]), ProjectPath: path}, nil
}
func (s Scope) valid() bool {
	if s.Kind == "user" {
		return s == UserScope()
	}
	if s.Kind != "project" || !filepath.IsAbs(s.ProjectPath) {
		return false
	}
	sum := sha256.Sum256([]byte(s.ProjectPath))
	return s.ProjectKey == hex.EncodeToString(sum[:]) && s.ProjectPath == filepath.Clean(s.ProjectPath)
}

// Thread is the canonical ThreadRef URL. Limited locators explicitly say when
// the backend did not provide a reusable message ID.
type Source struct {
	LocalTurnID  string    `json:"localTurnId,omitempty"`
	Thread       string    `json:"thread,omitempty"`
	MessageID    string    `json:"messageId,omitempty"`
	TurnID       string    `json:"turnId,omitempty"`
	Locator      string    `json:"locator,omitempty"`
	Limited      bool      `json:"limited,omitempty"`
	Quote        string    `json:"quote,omitempty"`
	EvidenceKind string    `json:"evidenceKind"`
	ObservedAt   time.Time `json:"observedAt"`
}

type Entry struct {
	ID         string    `json:"id"`
	Version    uint64    `json:"version"`
	Scope      Scope     `json:"scope"`
	Kind       string    `json:"kind"`
	Content    string    `json:"content,omitempty"`
	TaskKey    string    `json:"taskKey,omitempty"`
	Sources    []Source  `json:"sources,omitempty"`
	ObservedAt time.Time `json:"observedAt,omitzero"`
	ValidUntil time.Time `json:"validUntil,omitzero"`
	State      string    `json:"state"`
	Supersedes uint64    `json:"supersedes,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// UnmarshalJSON replaces an entry, rather than merging omitted fields into
// a previous version. A forgotten response must clear a reused client's body
// and evidence even though those fields are absent from the stored JSON.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type wire Entry
	var fresh wire
	if err := json.Unmarshal(data, &fresh); err != nil {
		return err
	}
	*e = Entry(fresh)
	return nil
}

// Disabled affects automatic use and MCP, never the management interface.
// Automatic features default off and are enabled independently per project.
type Settings struct {
	Disabled           bool   `json:"disabled,omitempty"`
	UseUserPreferences bool   `json:"useUserPreferences,omitempty"`
	AutoInject         bool   `json:"autoInject,omitempty"`
	AutoExtract        bool   `json:"autoExtract,omitempty"`
	ExtractorAgent     string `json:"extractorAgent,omitempty"`
}

func (s Settings) ExtractionAgent() string {
	if s.ExtractorAgent == "" {
		return "claude-code"
	}
	return s.ExtractorAgent
}

type Mutation struct {
	ID                     string
	Version                uint64
	Kind, Content, TaskKey string
	Sources                []Source
	ObservedAt, ValidUntil time.Time
}

type ConflictError struct{ Current Entry }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("Memory version changed: %s is now version %d. Query again.", e.Current.ID, e.Current.Version)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validate(m Mutation, scope Scope) error {
	if (m.ID == "") != (m.Version == 0) {
		return errors.New("Modifications require both id and version")
	}
	if m.Kind != "preference" && m.Kind != "fact" && m.Kind != "progress" {
		return errors.New("kind must be preference, fact, or progress")
	}
	if scope.Kind == "user" && m.Kind != "preference" {
		return errors.New("User scope only retains preferences explicitly chosen by the user")
	}
	if strings.TrimSpace(m.Content) == "" || utf8.RuneCountInString(m.Content) > 2000 {
		return errors.New("Memory content must be 1–2000 characters")
	}
	if utf8.RuneCountInString(m.TaskKey) > 200 {
		return errors.New("Task identifier is too long")
	}
	if m.Kind == "progress" && strings.TrimSpace(m.TaskKey) == "" {
		return errors.New("Progress requires task_key")
	}
	if m.ObservedAt.IsZero() || len(m.Sources) == 0 || len(m.Sources) > 8 {
		return errors.New("Memory requires timestamps and sources (up to 8)")
	}
	for _, s := range m.Sources {
		if s.ObservedAt.IsZero() || utf8.RuneCountInString(s.Quote) > 1000 {
			return errors.New("Source timestamp missing or excerpt too long")
		}
		switch s.EvidenceKind {
		case "user_statement", "assistant_report", "tool_result":
			if s.Thread == "" || (s.MessageID == "" && s.Locator == "" && s.TurnID == "") || strings.TrimSpace(s.Quote) == "" {
				return errors.New("Session source is missing its identity, location, or original excerpt")
			}
		case "manual": // A direct edit in the user's management interface.
		default:
			return errors.New("Invalid evidence category")
		}
	}
	return nil
}

func effective(e Entry, now time.Time) Entry {
	if e.State == EntryActive && !e.ValidUntil.IsZero() && !now.Before(e.ValidUntil) {
		e.State = EntryExpired
	}
	return e
}
