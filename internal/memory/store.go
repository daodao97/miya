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
	Format     int         `json:"format"`
	Scope      Scope       `json:"scope"`
	Revision   uint64      `json:"revision"`
	Settings   Settings    `json:"settings"`
	Entries    []Entry     `json:"entries"`
	Tombstones []tombstone `json:"tombstones,omitempty"`
}
type cached struct {
	doc document
	err error
}

type Store struct {
	mu     sync.Mutex
	root   string
	scopes map[Scope]cached
	now    func() time.Time
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
		return document{}, errors.New("无效记忆作用域")
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
			err = errors.New("记忆格式版本或作用域不匹配")
		}
		if err == nil {
			err = validateDocument(d)
		}
	}
	if err != nil {
		err = fmt.Errorf("记忆作用域不可用（原文件保留）：%w", err)
	}
	s.scopes[scope] = cached{doc: d, err: err}
	return clone(d), err
}
func validateDocument(d document) error {
	seen := map[string]uint64{}
	for _, e := range d.Entries {
		if e.Scope != d.Scope || e.ID == "" || e.Version == 0 || seen[e.ID] >= e.Version {
			return errors.New("记忆条目身份或版本无效")
		}
		seen[e.ID] = e.Version
		switch e.State {
		case "active", "superseded", "forgotten", "promoted":
		default:
			return errors.New("未知记忆状态")
		}
		if e.State != "forgotten" {
			if err := validate(Mutation{ID: e.ID, Version: e.Version, Kind: e.Kind, Content: e.Content, TaskKey: e.TaskKey, Sources: e.Sources, ObservedAt: e.ObservedAt, ValidUntil: e.ValidUntil}, d.Scope); err != nil {
				return err
			}
		}
		if e.State == "forgotten" && (e.Content != "" || len(e.Sources) != 0) {
			return errors.New("遗忘条目仍含正文")
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
		return fmt.Errorf("记忆未保存：%w", err)
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
	if d.Entries[i].State != "active" {
		return 0, fmt.Errorf("记忆已%s，不能修改", d.Entries[i].State)
	}
	return i, nil
}
func targetVersion(d document, id string, version uint64) (int, error) {
	i, ok := find(d, id)
	if !ok {
		return 0, errors.New("记忆不存在于此作用域")
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
	d, err := s.load(scope)
	if err != nil {
		return err
	}
	if settings.AutoInject || settings.AutoExtract {
		return errors.New("自动注入和自动提取尚未启用")
	}
	d.Settings = settings
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
	e := Entry{ID: m.ID, Version: 1, Scope: scope, Kind: m.Kind, Content: m.Content, TaskKey: m.TaskKey, Sources: m.Sources, ObservedAt: m.ObservedAt, ValidUntil: m.ValidUntil, State: "active", CreatedAt: now, UpdatedAt: now}
	if m.ID != "" {
		i, err := target(d, m.ID, m.Version)
		if err != nil {
			return Entry{}, err
		}
		old := d.Entries[i]
		d.Entries[i].State = "superseded"
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
	d, err := s.load(scope)
	if err != nil {
		return Entry{}, err
	}
	i, err := targetVersion(d, id, version)
	if err != nil {
		return Entry{}, err
	}
	old := d.Entries[i]
	if old.State == "forgotten" {
		return Entry{}, errors.New("记忆已忘记")
	}
	mark := tombstone{ID: id, Version: version, Revision: d.Revision + 1}
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
	e := Entry{ID: id, Version: version + 1, Scope: scope, State: "forgotten", CreatedAt: old.CreatedAt, UpdatedAt: s.now()}
	d.Entries = append(keep, e)
	d.Tombstones = append(d.Tombstones, mark)
	// The tombstone and removal commit atomically. Future derived records must
	// pass this barrier before reading a forgotten version.
	if err = s.commit(d); err != nil {
		return Entry{}, err
	}
	return e, nil
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
	if before != nil {
		if err := before(); err != nil {
			return Entry{}, err
		}
	}
	e := d.Entries[i]
	e.Version++
	e.Supersedes = version
	e.State = "promoted"
	e.UpdatedAt = s.now()
	d.Entries[i].State = "superseded"
	d.Entries = append(d.Entries, e)
	if err = s.commit(d); err != nil {
		if before != nil {
			return Entry{}, fmt.Errorf("项目文件已写入，但记忆状态未保存，请刷新检查：%w", err)
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
		return Entry{}, errors.New("只能提升项目偏好")
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
	if old.Kind != "preference" || old.State != "active" {
		return Entry{}, errors.New("只有有效的项目偏好可以提升到用户级")
	}
	e, err := s.save(UserScope(), Mutation{Kind: old.Kind, Content: old.Content, TaskKey: old.TaskKey, Sources: old.Sources, ObservedAt: old.ObservedAt, ValidUntil: old.ValidUntil})
	if err != nil {
		return Entry{}, err
	}
	if _, err = s.promote(scope, id, version, nil); err != nil {
		return e, fmt.Errorf("用户偏好已保存为 %s，但原记忆标记失败，请刷新检查：%w", e.ID, err)
	}
	return e, nil
}
