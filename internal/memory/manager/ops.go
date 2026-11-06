package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/textutil"
)

type Request struct {
	Project         string           `json:"project,omitempty"`
	Scope           string           `json:"scope,omitempty"`
	ID              string           `json:"id,omitempty"`
	Version         uint64           `json:"version,omitempty"`
	Kind            string           `json:"kind,omitempty"`
	Content         string           `json:"content,omitempty"`
	TaskKey         string           `json:"taskKey,omitempty"`
	ValidUntil      string           `json:"validUntil,omitempty"`
	Settings        *memory.Settings `json:"settings,omitempty"`
	SessionDisabled *bool            `json:"sessionDisabled,omitempty"`
	Thread          int              `json:"thread,omitempty"`
	File            string           `json:"file,omitempty"`
	// Promotion is a two-step preview/commit, with exact proposed file bytes.
	Preview string `json:"preview,omitempty"`
}

type List struct {
	Entries            []memory.Entry          `json:"entries"`
	Settings           memory.Settings         `json:"settings"`
	Revision           uint64                  `json:"revision"`
	Extraction         memory.ExtractionStatus `json:"extraction"`
	ExtractorAvailable bool                    `json:"extractorAvailable"`
	Extractors         []ExtractorOption       `json:"extractors,omitempty"`
}

type ExtractorOption struct {
	Agent     string `json:"agent"`
	Available bool   `json:"available"`
}

type Promotion struct {
	File   string `json:"file"`
	Before string `json:"before"`
	After  string `json:"after"`
}

func (mg *Manager) Note(thread int, e memory.Entry) {
	mg.Host.Changed()
	if thread == 0 {
		return
	}
	mg.Host.UpdateThread(thread, func(t *model.Thread) {
		// The notice contains only identity; its body is read on demand, so
		// forgetting leaves no extra plaintext in histories or state broadcasts.
		t.Messages = append(t.Messages, model.Message{ID: mg.Host.NextID(), Role: "memory", Kind: "memory", SentAtMs: time.Now().UnixMilli(), Memory: &model.MemoryNotice{Explicit: true, Scope: e.Scope.Kind, Project: e.Scope.ProjectPath, ID: e.ID, Version: e.Version}})
	})
}

// RootProject reports a filesystem root. An app opened from Finder
// without a workspace starts in "/", which is not a project: settings saved
// there never apply to the conversations the user is actually running.
func RootProject(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	clean := filepath.Clean(path)
	return filepath.Dir(clean) == clean
}

func (mg *Manager) Scope(r Request) (memory.Scope, error) {
	if r.Scope == "user" {
		return memory.UserScope(), nil
	}
	if r.Scope != "" && r.Scope != "project" {
		return memory.Scope{}, errors.New("Invalid memory scope")
	}
	cwd, paths := mg.Host.Projects()
	project := r.Project
	if project == "" {
		project = cwd
	}
	if RootProject(project) {
		return memory.Scope{}, errors.New("Open a project before using project memory")
	}
	known := project == cwd
	for _, p := range paths {
		if p == project {
			known = true
		}
	}
	if !known {
		return memory.Scope{}, errors.New("Project does not exist")
	}
	return memory.ProjectScope(project)
}

func (mg *Manager) Op(op string, r Request) (any, error) {
	if mg.Store == nil {
		return nil, errors.New("Memory requires the Miya background service")
	}
	scope, err := mg.Scope(r)
	if err != nil {
		return nil, err
	}
	store := mg.Store
	if op == "memorySessionConfigure" {
		t := mg.Host.Thread(r.Thread)
		threadScope, _ := memory.ProjectScope(mg.Host.Workspace(t.ID))
		if t.ID == 0 || r.SessionDisabled == nil || scope.Kind != "project" || threadScope != scope {
			return nil, errors.New("Session settings missing for the current project")
		}
		disabled := *r.SessionDisabled
		session := mg.Host.ThreadRef(t).URL()
		return mg.Later(func() (any, error) {
			if err := store.ConfigureSession(scope, session, disabled); err != nil {
				return nil, err
			}
			mg.Host.Dispatch(func() {
				mg.Host.UpdateThread(t.ID, func(t *model.Thread) { t.MemoryDisabled = disabled })
				if disabled {
					delete(mg.Turns, t.ID)
				}
				mg.Host.Changed()
			})
			return nil, nil
		}), nil
	}
	claudeAvailable := mg.Host.ClaudeBinary() != ""
	// Resolve the target conversation before leaving the scheduler.
	if r.Thread != 0 && mg.Host.Thread(r.Thread).ID == 0 {
		return nil, errors.New("Session does not exist")
	}
	return mg.Later(func() (any, error) {
		entries, settings, revision, err := store.List(scope)
		if err != nil {
			return nil, err
		}
		_, codexErr := codex.Binary()
		extractors := []ExtractorOption{{Agent: model.AgentCodex, Available: codexErr == nil}, {Agent: model.AgentClaude, Available: claudeAvailable}}
		available := func(agent string) bool {
			for _, option := range extractors {
				if option.Agent == agent {
					return option.Available
				}
			}
			return false
		}
		if op == "memoryList" {
			status, _ := store.ExtractionStatus(scope)
			return List{Entries: entries, Settings: settings, Revision: revision, Extraction: status, ExtractorAvailable: available(settings.ExtractionAgent()), Extractors: extractors}, nil
		}
		if op == "memoryConfigure" {
			if r.Settings == nil || scope.Kind != "project" {
				return nil, errors.New("Project memory settings missing")
			}
			selected := r.Settings.ExtractionAgent()
			if r.Settings.AutoExtract && !r.Settings.Disabled && !available(selected) && !(settings.AutoExtract && settings.ExtractionAgent() == selected) {
				return nil, fmt.Errorf("Automatic extraction requires a working %s configuration", model.AgentName(selected))
			}
			err := store.Configure(scope, *r.Settings)
			if err == nil {
				mg.Host.Dispatch(mg.Host.Changed)
			}
			return nil, err
		}
		var old memory.Entry
		for _, e := range entries {
			if e.ID == r.ID {
				old = e
				break
			}
		}
		if op == "memoryGet" {
			if old.ID == "" {
				return nil, errors.New("Memory does not exist")
			}
			if r.Version != old.Version {
				old.Content = ""
				old.Sources = nil
				if old.State == "active" || old.State == "expired" {
					old.State = "superseded"
				}
			}
			return old, nil
		}
		if r.ID != "" && (old.ID == "" || old.Version != r.Version) {
			return nil, fmt.Errorf("Memory changed. Refresh and retry (current version %d).", old.Version)
		}
		var e memory.Entry
		switch op {
		case "memorySave":
			until, err := Expiry(r.ValidUntil)
			if err != nil {
				return nil, err
			}
			now := time.Now()
			sources := append([]memory.Source(nil), old.Sources...)
			if len(sources) >= 8 {
				sources = append(sources[:1:1], sources[len(sources)-6:]...)
			}
			sources = append(sources, memory.Source{EvidenceKind: "manual", ObservedAt: now})
			e, err = store.Save(scope, memory.Mutation{ID: r.ID, Version: r.Version, Kind: r.Kind, Content: strings.TrimSpace(r.Content), TaskKey: r.TaskKey, Sources: sources, ObservedAt: now, ValidUntil: until})
		case "memoryForget":
			e, err = store.Forget(scope, r.ID, r.Version)
		case "memoryDelete":
			if err = store.Delete(scope, r.ID, r.Version); err == nil {
				e = memory.Entry{ID: r.ID}
			}
		case "memoryPromoteUser":
			e, err = store.PromotePreference(scope, r.ID, r.Version)
		case "memoryPromotionPreview", "memoryPromoteFile":
			if scope.Kind != "project" || old.State != "active" || old.Kind != "fact" {
				return nil, errors.New("Only active project facts can be promoted to project files")
			}
			if r.File != "AGENTS.md" && r.File != "CLAUDE.md" {
				return nil, errors.New("Select AGENTS.md or CLAUDE.md")
			}
			promotion, err := PreviewFile(scope.ProjectPath, r.File, textutil.FirstNonEmpty(strings.TrimSpace(r.Content), old.Content))
			if err != nil {
				return nil, err
			}
			if op == "memoryPromotionPreview" {
				return promotion, nil
			}
			var approved Promotion
			if json.Unmarshal([]byte(r.Preview), &approved) != nil || approved != promotion {
				return nil, errors.New("Project file or memory changed. Preview and confirm again.")
			}
			// Serialize promotion with all memory mutations, validating the version
			// again immediately before the file write and memory commit.
			e, err = store.PromoteWith(scope, r.ID, r.Version, func() error { return WriteProjectFile(scope.ProjectPath, promotion) })
		default:
			return nil, errors.New("Unknown memory operation")
		}
		if err != nil {
			if e.ID != "" {
				mg.Host.Dispatch(mg.Host.Changed)
			}
			return nil, err
		}
		if e.ID != "" {
			mg.Host.Dispatch(mg.Host.Changed)
		}
		return e, nil
	}), nil
}

func PreviewFile(project, file, content string) (Promotion, error) {
	path := filepath.Join(project, file)
	// Avoid silently following a link to a different project's instructions.
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return Promotion{}, errors.New("The target must be a regular file within the project, not a symbolic link")
	}
	if err != nil && !os.IsNotExist(err) {
		return Promotion{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Promotion{}, err
	}
	before := string(b)
	after := strings.TrimRight(before, "\n") + "\n\n" + content + "\n"
	if before == "" {
		after = content + "\n"
	}
	return Promotion{File: file, Before: before, After: after}, nil
}

func WriteProjectFile(project string, p Promotion) error {
	now, err := PreviewFile(project, p.File, "")
	if err != nil {
		return err
	}
	if now.Before != p.Before {
		return errors.New("Project file changed. Preview again.")
	}
	path := filepath.Join(project, p.File)
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.CreateTemp(project, ".memory-promotion-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.WriteString(p.After); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Recheck after preparing and flushing the replacement, too.
	latest, err := PreviewFile(project, p.File, "")
	if err != nil {
		return err
	}
	if latest.Before != p.Before {
		return errors.New("Project file changed. Preview again.")
	}
	return os.Rename(f.Name(), path)
}

// Count the accepted operation before it leaves the scheduler, so an upgrade
// waits for commits already in flight, including a file promotion.
func (mg *Manager) Later(work func() (any, error)) async.Reply {
	mg.Calls++
	return func() (any, error) { defer mg.Host.Dispatch(func() { mg.Calls-- }); return work() }
}
