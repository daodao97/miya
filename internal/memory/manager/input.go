package manager

import (
	"fmt"
	"strings"
	"unicode"

	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/memory"
)

// Memory preparation is called from backend workers, never the service's main
// scheduler. req.Text retains only the user text and explicit reference context.
type Input struct {
	contact  *contacts.Session
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
	base := Input{contact: t.Contact}
	if Command(req.Text) {
		base.contact = nil
	}
	if mg.Store == nil || t.MemoryDisabled || t.Automation != "" || req.MemoryControl || req.KeepSessionSettings || Command(req.Text) {
		return base
	}
	scope, err := memory.ProjectScope(project)
	if t.Contact != nil && t.Contact.Daily {
		scope, err = memory.UserScope(), nil
	}
	if err != nil {
		return base
	}
	own, _ := model.SplitReferenceContext(req.Text)
	p, err := mg.Store.Prepare(scope, session, fmt.Sprint(t.MemoryEpoch), own, TaskKey(t, session))
	if err != nil {
		return base
	}
	base.store, base.scope, base.prepared = mg.Store, scope, p
	return base
}

func (m Input) Text(raw string) string {
	raw = contacts.Input(m.contact, raw)
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
