package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/codex"
	"miya/internal/memory"
)

type memoryRequest struct {
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
type memoryList struct {
	Entries            []memory.Entry          `json:"entries"`
	Settings           memory.Settings         `json:"settings"`
	Revision           uint64                  `json:"revision"`
	Extraction         memory.ExtractionStatus `json:"extraction"`
	ExtractorAvailable bool                    `json:"extractorAvailable"`
	Extractors         []memoryExtractorOption `json:"extractors,omitempty"`
}

type memoryExtractorOption struct {
	Agent     string `json:"agent"`
	Available bool   `json:"available"`
}
type memoryPromotion struct {
	File   string `json:"file"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type memoryNotice struct {
	Explicit bool   `json:"explicit,omitempty"`
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	ID       string `json:"id"`
	Version  uint64 `json:"version"`
}

func (c *chatController) noteMemory(thread int, e memory.Entry) {
	c.memoryChanged()
	if thread == 0 {
		return
	}
	c.update(thread, func(t *Thread) {
		// The notice contains only identity; its body is read on demand, so
		// forgetting leaves no extra plaintext in histories or state broadcasts.
		t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "memory", Kind: "memory", SentAtMs: time.Now().UnixMilli(), Memory: &memoryNotice{Explicit: true, Scope: e.Scope.Kind, Project: e.Scope.ProjectPath, ID: e.ID, Version: e.Version}})
	})
}
func (c *chatController) memoryScope(r memoryRequest) (memory.Scope, error) {
	if r.Scope == "user" {
		return memory.UserScope(), nil
	}
	if r.Scope != "" && r.Scope != "project" {
		return memory.Scope{}, errors.New("无效记忆作用域")
	}
	project := r.Project
	if project == "" {
		project = c.cwd
	}
	known := project == c.cwd
	if c.projects != nil {
		for _, p := range c.projects() {
			if p.Path == project {
				known = true
			}
		}
	}
	if !known {
		return memory.Scope{}, errors.New("项目不存在")
	}
	return memory.ProjectScope(project)
}
func (c *chatController) memoryOp(op string, r memoryRequest) (any, error) {
	if c.memory == nil {
		return nil, errors.New("记忆需要 Miya 后台服务")
	}
	scope, err := c.memoryScope(r)
	if err != nil {
		return nil, err
	}
	store := c.memory
	if op == "memorySessionConfigure" {
		t := c.threadWithID(r.Thread)
		threadScope, _ := memory.ProjectScope(c.threadWorkspace(t.ID))
		if t.ID == 0 || r.SessionDisabled == nil || scope.Kind != "project" || threadScope != scope {
			return nil, errors.New("缺少当前项目的会话设置")
		}
		disabled := *r.SessionDisabled
		session := c.threadRef(t).URL()
		return c.memoryDeferred(func() (any, error) {
			if err := store.ConfigureSession(scope, session, disabled); err != nil {
				return nil, err
			}
			c.dispatch(func() {
				c.update(t.ID, func(t *Thread) { t.MemoryDisabled = disabled })
				if disabled {
					delete(c.memoryTurns, t.ID)
				}
				c.memoryChanged()
			})
			return nil, nil
		}), nil
	}
	claudeAvailable := c.claudeBinary != ""
	// Resolve the target conversation before leaving the scheduler.
	if r.Thread != 0 && c.threadWithID(r.Thread).ID == 0 {
		return nil, errors.New("会话不存在")
	}
	return c.memoryDeferred(func() (any, error) {
		entries, settings, revision, err := store.List(scope)
		if err != nil {
			return nil, err
		}
		_, codexErr := codex.Binary()
		extractors := []memoryExtractorOption{{Agent: agentCodex, Available: codexErr == nil}, {Agent: agentClaude, Available: claudeAvailable}}
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
			return memoryList{Entries: entries, Settings: settings, Revision: revision, Extraction: status, ExtractorAvailable: available(settings.ExtractionAgent()), Extractors: extractors}, nil
		}
		if op == "memoryConfigure" {
			if r.Settings == nil || scope.Kind != "project" {
				return nil, errors.New("缺少项目记忆设置")
			}
			selected := r.Settings.ExtractionAgent()
			if r.Settings.AutoExtract && !r.Settings.Disabled && !available(selected) && !(settings.AutoExtract && settings.ExtractionAgent() == selected) {
				return nil, fmt.Errorf("自动提炼需要先配置可用的 %s", backendFor(selected).Name())
			}
			err := store.Configure(scope, *r.Settings)
			if err == nil {
				c.dispatch(c.memoryChanged)
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
				return nil, errors.New("记忆不存在")
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
			return nil, fmt.Errorf("记忆已变化，请刷新后重试（当前版本 %d）", old.Version)
		}
		var e memory.Entry
		switch op {
		case "memorySave":
			until, err := memoryExpiry(r.ValidUntil)
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
		case "memoryPromoteUser":
			e, err = store.PromotePreference(scope, r.ID, r.Version)
		case "memoryPromotionPreview", "memoryPromoteFile":
			if scope.Kind != "project" || old.State != "active" || old.Kind != "fact" {
				return nil, errors.New("只有有效的项目事实可以提升到项目文件")
			}
			if r.File != "AGENTS.md" && r.File != "CLAUDE.md" {
				return nil, errors.New("请选择 AGENTS.md 或 CLAUDE.md")
			}
			promotion, err := previewMemoryFile(scope.ProjectPath, r.File, firstNonEmpty(strings.TrimSpace(r.Content), old.Content))
			if err != nil {
				return nil, err
			}
			if op == "memoryPromotionPreview" {
				return promotion, nil
			}
			var approved memoryPromotion
			if json.Unmarshal([]byte(r.Preview), &approved) != nil || approved != promotion {
				return nil, errors.New("项目文件或记忆已变化，请重新预览并确认")
			}
			// Serialize promotion with all memory mutations, validating the version
			// again immediately before the file write and memory commit.
			e, err = store.PromoteWith(scope, r.ID, r.Version, func() error { return writeMemoryProjectFile(scope.ProjectPath, promotion) })
		default:
			return nil, errors.New("未知记忆操作")
		}
		if err != nil {
			if e.ID != "" {
				c.dispatch(c.memoryChanged)
			}
			return nil, err
		}
		if e.ID != "" {
			c.dispatch(c.memoryChanged)
		}
		return e, nil
	}), nil
}
func previewMemoryFile(project, file, content string) (memoryPromotion, error) {
	path := filepath.Join(project, file)
	// Avoid silently following a link to a different project's instructions.
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return memoryPromotion{}, errors.New("目标必须是项目内的普通文件，不能是符号链接")
	}
	if err != nil && !os.IsNotExist(err) {
		return memoryPromotion{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return memoryPromotion{}, err
	}
	before := string(b)
	after := strings.TrimRight(before, "\n") + "\n\n" + content + "\n"
	if before == "" {
		after = content + "\n"
	}
	return memoryPromotion{File: file, Before: before, After: after}, nil
}
func writeMemoryProjectFile(project string, p memoryPromotion) error {
	now, err := previewMemoryFile(project, p.File, "")
	if err != nil {
		return err
	}
	if now.Before != p.Before {
		return errors.New("项目文件已变化，请重新预览")
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
	latest, err := previewMemoryFile(project, p.File, "")
	if err != nil {
		return err
	}
	if latest.Before != p.Before {
		return errors.New("项目文件已变化，请重新预览")
	}
	return os.Rename(f.Name(), path)
}

// Count the accepted operation before it leaves the scheduler, so an upgrade
// waits for commits already in flight, including a file promotion.
func (c *chatController) memoryDeferred(work func() (any, error)) deferred {
	c.memoryCalls++
	return func() (any, error) { defer c.dispatch(func() { c.memoryCalls-- }); return work() }
}
func (c *chatController) memoryChanged() {
	c.memoryRevision++
	if c.revision != nil {
		c.setRevision(c.revision() + 1)
	}
}
