package main

import (
	"fmt"
	"strings"
	"unicode"

	"miya/internal/memory"
)

// Memory preparation is called from backend workers, never the service's main
// scheduler. req.Text retains only the user text and explicit reference context.
type memoryInput struct {
	store    *memory.Store
	scope    memory.Scope
	prepared memory.Prepared
}

// Only command-shaped first tokens bypass memory. A leading absolute path is
// ordinary user input and must still participate in recall and extraction.
func memoryCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") || len(fields[0]) == 1 {
		return false
	}
	for _, r := range fields[0][1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != ':' {
			return false
		}
	}
	return true
}

func (c *chatController) prepareMemoryInput(t Thread, req SendRequest, project, session string) memoryInput {
	if c.memory == nil || t.MemoryDisabled || t.Automation != "" || req.MemoryControl || req.KeepSessionSettings || memoryCommand(req.Text) {
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
