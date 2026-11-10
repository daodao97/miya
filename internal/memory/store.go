package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type tombstone struct {
	ID       string   `json:"id"`
	Version  uint64   `json:"version"`
	Revision uint64   `json:"revision"`
	Digests  []string `json:"digests"`
	Sources  []Source `json:"sources,omitempty"`
}
type document struct {
	Format           int                      `json:"format"`
	Scope            Scope                    `json:"scope"`
	Revision         uint64                   `json:"revision"`
	Settings         Settings                 `json:"settings"`
	Entries          []Entry                  `json:"entries"`
	Tombstones       []tombstone              `json:"tombstones,omitempty"`
	Sessions         map[string]recallSession `json:"sessions,omitempty"`
	Injections       []Injection              `json:"injections,omitempty"`
	Jobs             []ExtractionJob          `json:"jobs,omitempty"`
	DisabledSessions map[string]bool          `json:"disabledSessions,omitempty"`
}
type cached struct {
	doc document
	err error
}

type Store struct {
	mu          sync.Mutex
	root        string
	scopes      map[Scope]cached
	searchIndex map[string]indexedTerms
	now         func() time.Time
	// replace is injectable for deterministic persistence-failure tests.
	replace func(string, []byte) error
}

func New(root string) *Store {
	return &Store{root: root, scopes: map[Scope]cached{}, now: time.Now, replace: atomicWrite}
}
func (s *Store) path(scope Scope) string {
	if scope.Kind == "user" {
		return filepath.Join(s.root, "user.json")
	}
	return filepath.Join(s.root, "projects", scope.ProjectKey+".json")
}
func clone(d document) document {
	b, _ := json.Marshal(d)
	var out document
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *Store) load(scope Scope) (document, error) {
	if !scope.valid() {
		return document{}, errors.New("Invalid memory scope")
	}
	if v, ok := s.scopes[scope]; ok {
		return clone(v.doc), v.err
	}
	d := document{Format: FormatVersion, Scope: scope, Entries: []Entry{}}
	b, err := os.ReadFile(s.path(scope))
	if os.IsNotExist(err) {
		err = nil
	} else if err == nil {
		err = json.Unmarshal(b, &d)
		if err == nil && (d.Format != FormatVersion || d.Scope != scope) {
			err = errors.New("Memory format version or scope mismatch")
		}
		if err == nil {
			err = validateDocument(d)
		}
	}
	if err != nil {
		err = fmt.Errorf("Memory scope unavailable (original file preserved): %w", err)
	}
	s.scopes[scope] = cached{doc: d, err: err}
	return clone(d), err
}

// read is a borrowed immutable view, valid only while the store lock is held.
// Mutations still use load's independent copy; searches copy only returned data.
func (s *Store) read(scope Scope) (document, error) {
	if v, ok := s.scopes[scope]; ok {
		return v.doc, v.err
	}
	_, err := s.load(scope)
	return s.scopes[scope].doc, err
}
func validateDocument(d document) error {
	if agent := d.Settings.ExtractionAgent(); agent != "codex" && agent != "claude-code" {
		return errors.New("Unknown memory extraction agent")
	}
	seen := map[string]uint64{}
	for _, e := range d.Entries {
		if e.Scope != d.Scope || e.ID == "" || e.Version == 0 || seen[e.ID] >= e.Version {
			return errors.New("Invalid memory identity or version")
		}
		seen[e.ID] = e.Version
		switch e.State {
		case EntryActive, EntrySuperseded, EntryForgotten, EntryPromoted, EntryConflicted:
		default:
			return errors.New("Unknown memory status")
		}
		if e.State != EntryForgotten {
			if err := validate(Mutation{ID: e.ID, Version: e.Version, Kind: e.Kind, Content: e.Content, TaskKey: e.TaskKey, Sources: e.Sources, ObservedAt: e.ObservedAt, ValidUntil: e.ValidUntil}, d.Scope); err != nil {
				return err
			}
		}
		if e.State == EntryForgotten && (e.Content != "" || len(e.Sources) != 0) {
			return errors.New("Forgotten entry still contains content")
		}
	}
	for _, j := range d.Jobs {
		if len(j.Key) != 64 || j.Session == "" || j.TurnID == "" || j.Attempts < 0 || j.Attempts > 2 {
			return errors.New("Invalid extraction task identity or retry count")
		}
		switch j.State {
		case JobPending, JobRunning:
			if len(j.Evidence) == 0 {
				return errors.New("Extraction task is missing source evidence")
			}
			for _, e := range j.Evidence {
				if e.ID == "" || e.Text == "" || e.Source.Thread != j.Session || e.Source.ObservedAt.IsZero() {
					return errors.New("Invalid extraction source")
				}
			}
		case JobCompleted, JobFailed, JobCanceled, JobStale:
			if len(j.Evidence) > 0 || len(j.Candidates) > 0 {
				return errors.New("Completed extraction task still contains evidence content")
			}
		default:
			return errors.New("Unknown extraction task status")
		}
	}
	return nil
}
func (s *Store) commit(d document) error {
	d.Revision++
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err = s.replace(s.path(d.Scope), b); err != nil {
		return fmt.Errorf("Memory was not saved: %w", err)
	}
	s.scopes[d.Scope] = cached{doc: clone(d)}
	return nil
}
func atomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Also protect an existing directory left with wider permissions.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if filepath.Base(dir) == "projects" {
		if err := os.Chmod(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
	}
	f, err := os.CreateTemp(dir, ".memory-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func latest(d document) []Entry {
	out := []Entry{}
	idx := map[string]int{}
	for _, e := range d.Entries {
		if i, ok := idx[e.ID]; ok {
			out[i] = e
		} else {
			idx[e.ID] = len(out)
			out = append(out, e)
		}
	}
	return out
}
func find(d document, id string) (int, bool) {
	for i := len(d.Entries) - 1; i >= 0; i-- {
		if d.Entries[i].ID == id {
			return i, true
		}
	}
	return 0, false
}
func target(d document, id string, version uint64) (int, error) {
	i, err := targetVersion(d, id, version)
	if err != nil {
		return 0, err
	}
	if d.Entries[i].State != EntryActive && d.Entries[i].State != EntryConflicted {
		return 0, fmt.Errorf("Memory is %s and cannot be modified", d.Entries[i].State)
	}
	return i, nil
}
func targetVersion(d document, id string, version uint64) (int, error) {
	i, ok := find(d, id)
	if !ok {
		return 0, errors.New("Memory does not exist in this scope")
	}
	e := d.Entries[i]
	if version == 0 || e.Version != version {
		return 0, &ConflictError{Current: e}
	}
	return i, nil
}
func (s *Store) List(scope Scope) ([]Entry, Settings, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(scope)
	if err != nil {
		return nil, Settings{}, 0, err
	}
	out := latest(d)
	for i := range out {
		out[i] = effective(out[i], s.now())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, d.Settings, d.Revision, nil
}
func (s *Store) Configure(scope Scope, settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agent := settings.ExtractionAgent(); agent != "claude-code" && agent != "codex" {
		return errors.New("Unsupported memory extraction agent")
	}
	d, err := s.load(scope)
	if err != nil {
		return err
	}
	d.Settings = settings
	if settings.Disabled || !settings.AutoExtract {
		for i := range d.Jobs {
			if d.Jobs[i].State.Open() {
				finishJob(&d.Jobs[i], JobCanceled, "Automatic extraction is disabled for this project", s.now())
			}
		}
	}
	return s.commit(d)
}
func (s *Store) Save(scope Scope, m Mutation) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(scope, m)
}
func (s *Store) save(scope Scope, m Mutation) (Entry, error) {
	if err := validate(m, scope); err != nil {
		return Entry{}, err
	}
	d, err := s.load(scope)
	if err != nil {
		return Entry{}, err
	}
	now := s.now()
	e := Entry{ID: m.ID, Version: 1, Scope: scope, Kind: m.Kind, Content: m.Content, TaskKey: m.TaskKey, Sources: m.Sources, ObservedAt: m.ObservedAt, ValidUntil: m.ValidUntil, State: EntryActive, CreatedAt: now, UpdatedAt: now}
	if m.ID != "" {
		i, err := target(d, m.ID, m.Version)
		if err != nil {
			return Entry{}, err
		}
		old := d.Entries[i]
		d.Entries[i].State = EntrySuperseded
		e.Version, e.Supersedes, e.CreatedAt = old.Version+1, old.Version, old.CreatedAt
	} else {
		e.ID, err = newID()
		if err != nil {
			return Entry{}, err
		}
	}
	if e.Kind == "progress" && e.ValidUntil.IsZero() {
		e.ValidUntil = e.ObservedAt.Add(7 * 24 * time.Hour)
	}
	d.Entries = append(d.Entries, e)
	if err = s.commit(d); err != nil {
		return Entry{}, err
	}
	// Don't expose the slices owned by the caller or the cache.
	return clone(document{Entries: []Entry{e}}).Entries[0], nil
}
func (s *Store) Forget(scope Scope, id string, version uint64) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchIndex = nil
	d, err := s.load(scope)
	if err != nil {
		return Entry{}, err
	}
	i, err := targetVersion(d, id, version)
	if err != nil {
		return Entry{}, err
	}
	if d.Entries[i].State == EntryForgotten {
		return Entry{}, errors.New("Memory forgotten")
	}
	e := s.forgetIn(&d, scope, i)
	// The tombstone and removal commit atomically. Future derived records must
	// pass this barrier before reading a forgotten version.
	if err = s.commit(d); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// forgetIn records the tombstone for entry i, removes every version's body
// and sources, and leaves a contentless forgotten record in its place.
func (s *Store) forgetIn(d *document, scope Scope, i int) Entry {
	old := d.Entries[i]
	id := old.ID
	mark := tombstone{ID: id, Version: old.Version, Revision: d.Revision + 1}
	keep := d.Entries[:0]
	for _, e := range d.Entries {
		if e.ID != id {
			keep = append(keep, e)
			continue
		}
		sum := sha256.Sum256([]byte(e.Content))
		mark.Digests = append(mark.Digests, hex.EncodeToString(sum[:]))
		for _, src := range e.Sources {
			src.Quote = ""
			mark.Sources = append(mark.Sources, src)
		}
	}
	e := Entry{ID: id, Version: old.Version + 1, Scope: scope, State: EntryForgotten, CreatedAt: old.CreatedAt, UpdatedAt: s.now()}
	d.Entries = append(keep, e)
	d.Tombstones = append(d.Tombstones, mark)
	scrubForgottenJobs(d, id, mark.Sources, s.now())
	return e
}

// Delete removes an entry from the list entirely. An entry that is not yet
// forgotten is forgotten first, so its tombstone still blocks old sources and
// bodies from being extracted again; only the contentless record is dropped.
// Sessions that saw it get an "unavailable" invalidation like any missing ID.
func (s *Store) Delete(scope Scope, id string, version uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchIndex = nil
	d, err := s.load(scope)
	if err != nil {
		return err
	}
	i, err := targetVersion(d, id, version)
	if err != nil {
		return err
	}
	if d.Entries[i].State != EntryForgotten {
		s.forgetIn(&d, scope, i)
	}
	keep := d.Entries[:0]
	for _, e := range d.Entries {
		if e.ID != id {
			keep = append(keep, e)
		}
	}
	d.Entries = keep
	return s.commit(d)
}
func (s *Store) Promote(scope Scope, id string, version uint64) (Entry, error) {
	return s.PromoteWith(scope, id, version, nil)
}

// PromoteWith checks the version under the writer lock before an approved file change.
func (s *Store) PromoteWith(scope Scope, id string, version uint64, before func() error) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.promote(scope, id, version, before)
}
func (s *Store) promote(scope Scope, id string, version uint64, before func() error) (Entry, error) {
	d, err := s.load(scope)
	if err != nil {
		return Entry{}, err
	}
	i, err := target(d, id, version)
	if err != nil {
		return Entry{}, err
	}
	if d.Entries[i].State != EntryActive {
		return Entry{}, errors.New("Only active memories can be promoted")
	}
	if before != nil {
		if err := before(); err != nil {
			return Entry{}, err
		}
	}
	e := d.Entries[i]
	e.Version++
	e.Supersedes = version
	e.State = EntryPromoted
	e.UpdatedAt = s.now()
	d.Entries[i].State = EntrySuperseded
	d.Entries = append(d.Entries, e)
	if err = s.commit(d); err != nil {
		if before != nil {
			return Entry{}, fmt.Errorf("Project file was written, but memory status was not saved. Refresh to check: %w", err)
		}
		return Entry{}, err
	}
	return e, nil
}

// PromotePreference serializes the two independently committed scope files.
// A partial commit returns the new ID instead of claiming both succeeded.
func (s *Store) PromotePreference(scope Scope, id string, version uint64) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope.Kind != "project" {
		return Entry{}, errors.New("Only project preferences can be promoted")
	}
	d, err := s.load(scope)
	if err != nil {
		return Entry{}, err
	}
	i, err := target(d, id, version)
	if err != nil {
		return Entry{}, err
	}
	old := effective(d.Entries[i], s.now())
	if old.Kind != "preference" || old.State != EntryActive {
		return Entry{}, errors.New("Only active project preferences can be promoted to user scope")
	}
	e, err := s.save(UserScope(), Mutation{Kind: old.Kind, Content: old.Content, TaskKey: old.TaskKey, Sources: old.Sources, ObservedAt: old.ObservedAt, ValidUntil: old.ValidUntil})
	if err != nil {
		return Entry{}, err
	}
	if _, err = s.promote(scope, id, version, nil); err != nil {
		return e, fmt.Errorf("User preference saved as %s, but the original memory could not be marked. Refresh to check: %w", e.ID, err)
	}
	return e, nil
}
