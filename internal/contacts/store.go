package contacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"miya/internal/fsutil"
	"miya/internal/textutil"
)

type receipt struct {
	Digest    string `json:"digest"`
	ProfileID string `json:"profileId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}
type document struct {
	Snapshot
	Receipts map[string]receipt `json:"receipts"`
}

type Store struct {
	mu     sync.Mutex
	root   string
	doc    document
	loaded bool
	err    error
}

func New(root string) *Store            { return &Store{root: root} }
func (s *Store) Daily(id string) string { return filepath.Join(s.root, "workspaces", id) }
func (s *Store) IsDaily(path string) bool {
	rel, err := filepath.Rel(filepath.Join(s.root, "workspaces"), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func clone(d document) document {
	b, _ := json.Marshal(d)
	var out document
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *Store) load() error {
	if s.loaded {
		return s.err
	}
	s.loaded = true
	s.doc = document{Snapshot: Snapshot{Version: Version, Profiles: []Profile{}, Sessions: []Session{}}, Receipts: map[string]receipt{}}
	b, err := os.ReadFile(filepath.Join(s.root, "contacts.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		var saved document
		err = json.Unmarshal(b, &saved)
		if err == nil {
			err = s.validate(saved)
		}
		if err == nil {
			s.doc = saved
		}
	}
	s.err = err
	return err
}

func (s *Store) validate(d document) error {
	invalid := errors.New("Invalid or unsupported contacts data. The original file has been preserved.")
	if d.Version != Version || d.Profiles == nil || d.Sessions == nil || d.Receipts == nil {
		return invalid
	}
	profiles := map[string]bool{}
	for _, p := range d.Profiles {
		if p.ID == "" || filepath.Base(p.ID) != p.ID || strings.ContainsAny(p.ID, "/\\") || profiles[p.ID] || p.Revision == 0 || p.PersonaVersion == 0 || Validate(p) != nil || !validAvatar(p.Avatar) {
			return invalid
		}
		profiles[p.ID] = true
	}
	sessions := map[string]bool{}
	native := map[string]bool{}
	for _, v := range d.Sessions {
		if v.ID == "" || sessions[v.ID] || !profiles[v.BotID] || v.PersonaVersion == 0 || v.Agent == "" || !filepath.IsAbs(v.Workspace) || filepath.Clean(v.Workspace) != v.Workspace {
			return invalid
		}
		if v.Daily && v.Workspace != s.Daily(v.BotID) {
			return invalid
		}
		key := v.Agent + "\x00" + v.NativeID
		if v.NativeID != "" && native[key] {
			return invalid
		}
		if v.NativeID != "" {
			native[key] = true
		}
		sessions[v.ID] = true
	}
	for _, r := range d.Receipts {
		if r.ProfileID != "" && !profiles[r.ProfileID] || r.SessionID != "" && !sessions[r.SessionID] {
			return invalid
		}
	}
	return nil
}
func (s *Store) save(d document) error {
	if err := s.validate(d); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err = fsutil.AtomicWriteFile(filepath.Join(s.root, "contacts.json"), b); err != nil {
		return err
	}
	s.doc = d
	return nil
}
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Snapshot{Version: Version, Error: err.Error()}
	}
	return clone(s.doc).Snapshot
}

// Apply commits the mutation and its receipt atomically. A timeout may be
// retried with the same request ID, including after a service restart.
func (s *Store) Apply(r Request) (Snapshot, *Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Snapshot{}, nil, err
	}
	if r.Op == "list" {
		return clone(s.doc).Snapshot, nil, nil
	}
	if len(r.ID) < 8 || len(r.ID) > 128 {
		return Snapshot{}, nil, errors.New("A stable request ID is required")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return Snapshot{}, nil, err
	}
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	if old, ok := s.doc.Receipts[r.ID]; ok {
		if old.Digest != digest {
			return Snapshot{}, nil, errors.New("Request ID already used with different content")
		}
		out := clone(s.doc).Snapshot
		for _, v := range out.Sessions {
			if v.ID == old.SessionID {
				if v.Hidden {
					return out, nil, errors.New("Conversation no longer exists")
				}
				return out, &v, nil
			}
		}
		return out, nil, nil
	}
	d := clone(s.doc)
	ack := receipt{Digest: digest}
	var session *Session
	switch r.Op {
	case "save":
		p := r.Profile
		p.Name, p.Description = strings.TrimSpace(p.Name), strings.TrimSpace(p.Description)
		if err := Validate(p); err != nil {
			return Snapshot{}, nil, err
		}
		index := -1
		for i, old := range d.Profiles {
			if old.ID == p.ID {
				index = i
				break
			}
		}
		if p.ID == "" {
			if p.Revision != 0 {
				return Snapshot{}, nil, errors.New("Invalid contact version")
			}
			p.ID, p.Revision, p.PersonaVersion = textutil.NewID(), 1, 1
		} else {
			if index < 0 {
				return Snapshot{}, nil, errors.New("Contact no longer exists")
			}
			old := d.Profiles[index]
			if p.Revision != old.Revision {
				return Snapshot{}, nil, errors.New("Contact changed on another device. Reload before saving.")
			}
			p.Revision, p.PersonaVersion = old.Revision+1, old.PersonaVersion
			if p.Persona != old.Persona || p.Agent != old.Agent || p.Model != old.Model || p.Effort != old.Effort {
				p.PersonaVersion++
			}
		}
		if len(r.AvatarData) != 0 {
			if len(r.AvatarData) > 512*1024 {
				return Snapshot{}, nil, errors.New("Avatar must be at most 512 KB")
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(r.AvatarData))
			if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
				return Snapshot{}, nil, errors.New("Choose a PNG, JPEG or GIF avatar up to 4096 pixels")
			}
			hash := sha256.Sum256(r.AvatarData)
			p.Avatar = "image:" + hex.EncodeToString(hash[:])
			if err := os.MkdirAll(filepath.Join(s.root, "avatars"), 0700); err != nil {
				return Snapshot{}, nil, err
			}
			if err := fsutil.AtomicWriteFile(filepath.Join(s.root, "avatars", strings.TrimPrefix(p.Avatar, "image:")), r.AvatarData); err != nil {
				return Snapshot{}, nil, err
			}
		} else if !validAvatar(p.Avatar) {
			return Snapshot{}, nil, errors.New("Invalid avatar")
		}
		if index < 0 {
			d.Profiles = append(d.Profiles, p)
		} else {
			d.Profiles[index] = p
		}
		ack.ProfileID = p.ID
	case "open":
		p, ok := d.Find(r.BotID)
		if !ok {
			return Snapshot{}, nil, errors.New("Contact no longer exists")
		}
		if r.Context != "recent" && r.Context != "daily" && r.Context != "project" {
			return Snapshot{}, nil, errors.New("Choose daily chat or a project")
		}
		var best *Session
		for _, v := range d.Sessions {
			if v.BotID != p.ID || v.Hidden {
				continue
			}
			if r.SessionID != "" {
				if v.ID == r.SessionID {
					best = &v
					break
				}
				continue
			}
			if r.New {
				continue
			}
			if r.Context == "daily" && !v.Daily || r.Context == "project" && (v.Daily || v.Workspace != r.Project) {
				continue
			}
			if best == nil || max(v.LastActivity, v.LastOpened) > max(best.LastActivity, best.LastOpened) {
				v := v
				best = &v
			}
		}
		if r.SessionID != "" && best == nil {
			return Snapshot{}, nil, errors.New("Conversation no longer exists")
		}
		if best == nil {
			if p.Archived {
				return Snapshot{}, nil, errors.New("Restore this contact before starting a conversation")
			}
			v := Session{ID: textutil.NewID(), BotID: p.ID, Name: p.Name, PersonaVersion: p.PersonaVersion, Persona: p.Persona, Agent: p.Agent, Model: p.Model, Effort: p.Effort, Daily: r.Context != "project", Workspace: r.Project, LastActivity: time.Now().UnixMilli()}
			if v.Daily {
				v.Workspace = s.Daily(p.ID)
			}
			if !filepath.IsAbs(v.Workspace) {
				return Snapshot{}, nil, errors.New("Workspace must be absolute")
			}
			if v.Daily {
				if err := os.MkdirAll(v.Workspace, 0700); err != nil {
					return Snapshot{}, nil, err
				}
			}
			d.Sessions = append(d.Sessions, v)
			best = &v
		}
		if best.Daily {
			if err := os.MkdirAll(best.Workspace, 0700); err != nil {
				return Snapshot{}, nil, err
			}
		}
		best.LastOpened = time.Now().UnixMilli()
		for _, existing := range d.Sessions {
			best.LastOpened = max(best.LastOpened, existing.LastOpened+1)
		}
		for i := range d.Sessions {
			if d.Sessions[i].ID == best.ID {
				d.Sessions[i].LastOpened = best.LastOpened
				break
			}
		}
		session, ack.SessionID = best, best.ID
	default:
		return Snapshot{}, nil, fmt.Errorf("Unknown contact operation %q", r.Op)
	}
	d.Receipts[r.ID] = ack
	d.Revision++
	if err := s.save(d); err != nil {
		return Snapshot{}, nil, err
	}
	return clone(d).Snapshot, session, nil
}

func validAvatar(v string) bool {
	if v == "" || v == "leaf" || v == "book" || v == "code" {
		return true
	}
	if !strings.HasPrefix(v, "image:") {
		return false
	}
	b, err := hex.DecodeString(strings.TrimPrefix(v, "image:"))
	return err == nil && len(b) == sha256.Size
}
func (s *Store) Avatar(ref string) ([]byte, error) {
	if !validAvatar(ref) || !strings.HasPrefix(ref, "image:") {
		return nil, errors.New("Avatar not found")
	}
	return os.ReadFile(filepath.Join(s.root, "avatars", strings.TrimPrefix(ref, "image:")))
}

func (s *Store) ProfileForRequest(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.load() != nil {
		return ""
	}
	return s.doc.Receipts[id].ProfileID
}

// SetChoices changes a conversation's explicit model choice, leaving the
// contact defaults and persona version untouched.
func (s *Store) SetChoices(id, modelID, effort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	d := clone(s.doc)
	for i := range d.Sessions {
		if d.Sessions[i].ID == id {
			if d.Sessions[i].Model == modelID && d.Sessions[i].Effort == effort {
				return nil
			}
			d.Sessions[i].Model, d.Sessions[i].Effort = modelID, effort
			d.Revision++
			return s.save(d)
		}
	}
	return errors.New("Contact conversation not found")
}

// Sync records acknowledged native identity and visible metadata. It never
// changes a frozen persona or reassigns a session to a different workspace.
func (s *Store) Sync(id, native string, ready bool, title, preview string, activity int64, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	d := clone(s.doc)
	for i, v := range d.Sessions {
		if v.ID != id {
			continue
		}
		old := v
		if native != "" {
			if v.NativeID != native {
				v.NativeReady = ready
			} else {
				v.NativeReady = v.NativeReady || ready
			}
			v.NativeID = native
		}
		v.Title, v.Preview, v.LastActivity, v.Hidden = title, preview, max(v.LastActivity, activity), hidden
		if v == old {
			return nil
		}
		d.Sessions[i] = v
		d.Revision++
		return s.save(d)
	}
	return errors.New("Contact conversation not found")
}
