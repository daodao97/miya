package controller

import (
	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/integrations"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/memory/manager"
)

func NewMemoryEngine(c *Controller) *extractor.Engine {
	cfg := claude.CLI{Binary: c.ClaudeBinary, Args: append([]string(nil), c.ClaudeArgs...), Env: append([]string(nil), c.ClaudeEnv...)}
	return extractor.NewEngine(c.parentContext(), c.Memories().Store, cfg, c.MemorySaved)
}

// memorySaved hears of the memories the engine saved. Automatic extraction
// updates the library, never the conversation.
func (c *Controller) MemorySaved(memory.Scope, memory.ExtractionJob, []memory.Entry) {
	c.Dispatch(c.memoryChanged)
}

// memories is the controller's memory feature, its host set on first use.
func (c *Controller) Memories() *manager.Manager {
	if c.mem.Host == nil {
		c.mem.Host = memoryHost{c}
	}
	return &c.mem
}

// memoryHost is the controller as the memory manager sees it.
type memoryHost struct{ c *Controller }

func (h memoryHost) Dispatch(f func()) { h.c.Dispatch(f) }

func (h memoryHost) Thread(id int) model.Thread { return h.c.ThreadWithID(id) }

func (h memoryHost) UpdateThread(id int, f func(*model.Thread)) { h.c.Update(id, f) }

func (h memoryHost) ThreadRef(t model.Thread) model.ThreadRef { return h.c.ThreadRef(t) }

func (h memoryHost) Workspace(thread int) string { return h.c.ThreadWorkspace(thread) }

func (h memoryHost) NextID() int { return h.c.NextID() }

func (h memoryHost) ClaudeBinary() string { return h.c.ClaudeBinary }

func (h memoryHost) Changed() { h.c.memoryChanged() }

func (h memoryHost) Projects() (string, []string) {
	var paths []string
	if h.c.Projects != nil {
		for _, p := range h.c.Projects() {
			paths = append(paths, p.Path)
		}
	}
	cwd := h.c.Cwd
	if h.c.isContactWorkspace(cwd) {
		cwd = ""
	}
	return cwd, paths
}

// memoryChanged tells windows the memory library changed.
func (c *Controller) memoryChanged() {
	c.MemoryRevision++
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}

func (h memoryHost) ExtractorAvailable(id string) bool { return integrations.BinaryAvailable(id) }
