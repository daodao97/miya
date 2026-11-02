package main

import (
	"fmt"
	"strings"

	"miya/internal/memory"
)

// Memory preparation is called from backend workers, never the service's main
// scheduler. req.Text retains only the user text and explicit reference context.
type memoryInput struct {
	store    *memory.Store
	scope    memory.Scope
	prepared memory.Prepared
}

func (c *chatController) prepareMemoryInput(t Thread, req sendRequest, project, session string) memoryInput {
	if c.memory == nil || t.MemoryDisabled || t.Automation != "" || req.MemoryControl || req.KeepSessionSettings || strings.HasPrefix(strings.TrimSpace(req.Text), "/") {
		return memoryInput{}
	}
	scope, err := memory.ProjectScope(project)
	if err != nil {
		return memoryInput{}
	}
	own, _ := splitReferenceContext(req.Text)
	p, err := c.memory.Prepare(scope, session, fmt.Sprint(t.MemoryEpoch), own, memoryTaskKey(t, session))
	if err != nil {
		return memoryInput{}
	}
	return memoryInput{c.memory, scope, p}
}

func (m memoryInput) text(raw string) string {
	if m.store == nil || m.prepared.Block == "" {
		return raw
	}
	if !m.store.ValidatePrepared(m.scope, m.prepared) {
		_ = m.store.FinishPrepared(m.scope, m.prepared, false)
		return raw
	}
	return raw + m.prepared.Block
}

func (m memoryInput) finish(delivered bool) {
	if m.store != nil {
		_ = m.store.FinishPrepared(m.scope, m.prepared, delivered)
	}
}
