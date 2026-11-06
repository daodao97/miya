package manager

import (
	"fmt"
	"strings"
	"unicode"

	"miya/internal/chat/model"
	"miya/internal/memory"
)

// Memory preparation is called from backend workers, never the service's main
// scheduler. req.Text retains only the user text and explicit reference context.
type Input struct {
	store    *memory.Store
	scope    memory.Scope
	prepared memory.Prepared
}

// Only command-shaped first tokens bypass memory. A leading absolute path is
// ordinary user input and must still participate in recall and extraction.
func Command(text string) bool {
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

func (mg *Manager) PrepareInput(t model.Thread, req model.SendRequest, project, session string) Input {
	if mg.Store == nil || t.MemoryDisabled || t.Automation != "" || req.MemoryControl || req.KeepSessionSettings || Command(req.Text) {
		return Input{}
	}
	scope, err := memory.ProjectScope(project)
	if err != nil {
		return Input{}
	}
	own, _ := model.SplitReferenceContext(req.Text)
	p, err := mg.Store.Prepare(scope, session, fmt.Sprint(t.MemoryEpoch), own, TaskKey(t, session))
	if err != nil {
		return Input{}
	}
	return Input{mg.Store, scope, p}
}

func (m Input) Text(raw string) string {
	if m.store == nil || m.prepared.Block == "" {
		return raw
	}
	if !m.store.ValidatePrepared(m.scope, m.prepared) {
		_ = m.store.FinishPrepared(m.scope, m.prepared, false)
		return raw
	}
	return raw + m.prepared.Block
}

func (m Input) Finish(delivered bool) {
	if m.store != nil {
		_ = m.store.FinishPrepared(m.scope, m.prepared, delivered)
	}
}
