// Package statecard keeps one state card per conversation: what the
// conversation is for, what is done, what remains and whether it waits for
// the person. The background service owns the cards. They are not memories:
// nothing recalls them, and a later card replaces the earlier one.
package statecard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"miya/internal/appdir"
)

// A card holds what cannot be looked up, in a few lines.
const (
	// MaxText is the length of the purpose, the question and each item.
	MaxText = 400
	// MaxItems is the length of each list.
	MaxItems = 12
	// MaxReference is the length of one reference.
	MaxReference = 500
	// MaxReferences is the number of references.
	MaxReferences = 20
)

// AuthorAgent marks a card the working agent wrote about its own conversation.
const AuthorAgent = "agent"

// AuthorBackground marks a card Miya produced from the conversation transcript.
const AuthorBackground = "background"

// ErrNoStore answers a write where no background service keeps the cards.
var ErrNoStore = errors.New("State cards require the Miya background service")

// Card is the state of one conversation, as its author last put it. Miya
// verifies none of it: Checked is the author's own account.
type Card struct {
	Backend string `json:"backend"`
	Session string `json:"session"`

	Purpose   string   `json:"purpose"`
	Done      []string `json:"done,omitempty"`
	Remaining []string `json:"remaining,omitempty"`
	// WaitingFor is what the person is asked; empty when nothing waits.
	WaitingFor string   `json:"waiting_for,omitempty"`
	Checked    []string `json:"checked,omitempty"`
	// References name commits, pull requests, files and session links.
	References []string `json:"references,omitempty"`

	Author      string `json:"author"`
	WrittenAtMs int64  `json:"written_at_ms"`
	// Turn is the turn that wrote the card.
	Turn string `json:"turn,omitempty"`
	// Item is the last message the card relied on: the call that wrote it.
	Item string `json:"item,omitempty"`
	// Stale says the conversation went on after the card was written.
	Stale bool `json:"stale,omitempty"`
}

// Dir is where the background service keeps the cards.
func Dir() string { return filepath.Join(appdir.Dir(), "status") }

// Key names the conversation of an agent's session. A conversation whose
// agent has not named its session yet has none.
func Key(backend, session string) string {
	if backend == "" || session == "" {
		return ""
	}
	return backend + "/" + session
}

func (c Card) Key() string { return Key(c.Backend, c.Session) }

// Waiting reports whether the card says the conversation waits for the
// person. An outdated card says nothing about now.
func (c Card) Waiting() bool { return c.WaitingFor != "" && !c.Stale }

// Normalize folds the whitespace of what the author wrote and drops empty
// items. A card that is too long is refused, not cut: the author shortens it.
func Normalize(c Card) (Card, error) {
	c.Purpose = oneLine(c.Purpose)
	c.WaitingFor = oneLine(c.WaitingFor)
	if c.Purpose == "" {
		return Card{}, errors.New("purpose is required: one sentence on what this conversation is for")
	}
	if utf8.RuneCountInString(c.Purpose) > MaxText {
		return Card{}, fmt.Errorf("purpose must be at most %d characters", MaxText)
	}
	if utf8.RuneCountInString(c.WaitingFor) > MaxText {
		return Card{}, fmt.Errorf("waiting_for must be at most %d characters", MaxText)
	}
	var err error
	if c.Done, err = list("done", c.Done, MaxItems, MaxText); err != nil {
		return Card{}, err
	}
	if c.Remaining, err = list("remaining", c.Remaining, MaxItems, MaxText); err != nil {
		return Card{}, err
	}
	if c.Checked, err = list("checked", c.Checked, MaxItems, MaxText); err != nil {
		return Card{}, err
	}
	if c.References, err = list("references", c.References, MaxReferences, MaxReference); err != nil {
		return Card{}, err
	}
	return c, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func list(name string, items []string, most, length int) ([]string, error) {
	var out []string
	for _, item := range items {
		if item = oneLine(item); item == "" {
			continue
		}
		if utf8.RuneCountInString(item) > length {
			return nil, fmt.Errorf("each item of %s must be at most %d characters", name, length)
		}
		out = append(out, item)
	}
	if len(out) > most {
		return nil, fmt.Errorf("%s accepts at most %d items", name, most)
	}
	return out, nil
}

// Store holds the cards, one small file each. A nil store has no cards.
type Store struct {
	mu    sync.Mutex
	dir   string
	cards map[string]Card
}

// Open loads the cards under dir. A file that is not a card is left alone
// and skipped: one broken card does not take the others with it.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, cards: map[string]Card{}}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c Card
		if json.Unmarshal(data, &c) != nil || c.Key() == "" || e.Name() != fileName(c.Key()) {
			continue
		}
		s.cards[c.Key()] = c
	}
	return s, nil
}

func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cards)
}

func (s *Store) Get(key string) (Card, bool) {
	if s == nil || key == "" {
		return Card{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[key]
	return c, ok
}

// Put saves c as the card of its conversation, in place of an earlier one.
func (s *Store) Put(c Card) error {
	if s == nil {
		return ErrNoStore
	}
	key := c.Key()
	if key == "" {
		return errors.New("The conversation has no session yet")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.write(c); err != nil {
		return err
	}
	s.cards[key] = c
	return nil
}

// SetStale marks the card at key as outdated or current, and reports
// whether that changed it.
func (s *Store) SetStale(key string, stale bool) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[key]
	if !ok || c.Stale == stale {
		return false, nil
	}
	c.Stale = stale
	if err := s.write(c); err != nil {
		return false, err
	}
	s.cards[key] = c
	return true, nil
}

// Move gives the card at from to the session its conversation continues in.
func (s *Store) Move(from, backend, session string) error {
	to := Key(backend, session)
	if s == nil || from == "" || to == "" || from == to {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[from]
	if !ok {
		return nil
	}
	c.Backend, c.Session = backend, session
	if err := s.write(c); err != nil {
		return err
	}
	s.cards[to] = c
	delete(s.cards, from)
	return s.remove(from)
}

// Delete drops the card of a conversation that is gone.
func (s *Store) Delete(key string) error {
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cards[key]; !ok {
		return nil
	}
	delete(s.cards, key)
	return s.remove(key)
}

func fileName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16]) + ".json"
}

func (s *Store) remove(key string) error {
	err := os.Remove(filepath.Join(s.dir, fileName(key)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// write replaces the card's file whole, so a reader never sees half of one.
func (s *Store) write(c Card) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".card-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(s.dir, fileName(c.Key())))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Line is the card in one line, for a list of conversations.
func Line(c Card) string {
	parts := []string{c.Purpose}
	if c.Author == AuthorBackground {
		parts = append(parts, "Summarized by Miya, unverified")
	}
	if len(c.Remaining) == 0 {
		parts = append(parts, "Nothing remaining")
	} else {
		shown, more := c.Remaining, ""
		if len(shown) > 3 {
			more = fmt.Sprintf(" (+%d more)", len(shown)-3)
			shown = shown[:3]
		}
		parts = append(parts, "Remaining: "+strings.Join(shown, "; ")+more)
	}
	if c.WaitingFor != "" {
		parts = append(parts, "Waiting for the person: "+c.WaitingFor)
	}
	if c.Stale {
		parts = append(parts, "Outdated")
	}
	return strings.Join(parts, " · ")
}

// Text is the whole card, for a reader that asked about one conversation.
// It says who wrote the card and that Miya did not verify it.
func Text(c Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "State card, written by %s at %s. Miya has not verified it.\n", writer(c), time.UnixMilli(c.WrittenAtMs).Format("2006-01-02 15:04"))
	if c.Stale {
		b.WriteString("Outdated: the conversation went on after it was written.\n")
	}
	b.WriteString("Purpose: " + c.Purpose + "\n")
	section(&b, "Done", c.Done)
	if len(c.Remaining) == 0 {
		b.WriteString("Remaining: nothing\n")
	} else {
		section(&b, "Remaining", c.Remaining)
	}
	if c.WaitingFor != "" {
		b.WriteString("Waiting for the person: " + c.WaitingFor + "\n")
	}
	section(&b, "Checked, by the writer's own account", c.Checked)
	section(&b, "References", c.References)
	return strings.TrimRight(b.String(), "\n")
}

func writer(c Card) string {
	if c.Author == AuthorAgent {
		return "the agent"
	}
	return "Miya"
}

func section(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString(title + ":\n")
	for _, item := range items {
		b.WriteString("- " + item + "\n")
	}
}
