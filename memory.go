package main

import (
	"miya/internal/claude"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/memory/manager"
	"miya/internal/model"
)

func newMemoryEngine(c *chatController) *extractor.Engine {
	cfg := claude.CLI{Binary: c.claudeBinary, Args: append([]string(nil), c.claudeArgs...), Env: append([]string(nil), c.claudeEnv...)}
	return extractor.NewEngine(c.parentContext(), c.memories().Store, cfg, c.memorySaved)
}

// memorySaved hears of the memories the engine saved. Automatic extraction
// updates the library, never the conversation.
func (c *chatController) memorySaved(memory.Scope, memory.ExtractionJob, []memory.Entry) {
	c.dispatch(c.memoryChanged)
}

// memories is the controller's memory feature, its host set on first use.
func (c *chatController) memories() *manager.Manager {
	if c.mem.Host == nil {
		c.mem.Host = memoryHost{c}
	}
	return &c.mem
}

// memoryHost is the controller as the memory manager sees it.
type memoryHost struct{ c *chatController }

func (h memoryHost) Dispatch(f func())                    { h.c.dispatch(f) }
func (h memoryHost) Thread(id int) Thread                 { return h.c.threadWithID(id) }
func (h memoryHost) UpdateThread(id int, f func(*Thread)) { h.c.update(id, f) }
func (h memoryHost) ThreadRef(t Thread) ThreadRef         { return h.c.threadRef(t) }
func (h memoryHost) Workspace(thread int) string          { return h.c.threadWorkspace(thread) }
func (h memoryHost) NextID() int                          { return h.c.nextID() }
func (h memoryHost) ClaudeBinary() string                 { return h.c.claudeBinary }
func (h memoryHost) AgentName(agent string) string        { return backendFor(agent).Name() }
func (h memoryHost) Changed()                             { h.c.memoryChanged() }
func (h memoryHost) OwnText(text string) string {
	own, _ := model.SplitReferenceContext(text)
	return own
}
func (h memoryHost) Projects() (string, []string) {
	var paths []string
	if h.c.projects != nil {
		for _, p := range h.c.projects() {
			paths = append(paths, p.Path)
		}
	}
	return h.c.cwd, paths
}

// memoryChanged tells windows the memory library changed.
func (c *chatController) memoryChanged() {
	c.memoryRevision++
	if c.revision != nil {
		c.setRevision(c.revision() + 1)
	}
}
