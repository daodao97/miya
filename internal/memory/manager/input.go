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
	// charter is the charter of the Bot whose conversation takes the message.
	// It goes with the message whether or not memory does.
	charter string
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
	base := Input{}
	if Command(req.Text) {
		// A command goes to the agent as typed.
		return base
	}
	base.charter = req.Charter
	if mg.Store == nil || req.KeepSessionSettings {
		return base
	}
	// A Bot recalls its own memory also in its tasks and automation runs,
	// which recall nothing of the project's.
	botOnly := t.MemoryDisabled || t.Automation != "" || req.MemoryControl
	if botOnly && req.BotID == "" {
		return base
	}
	scope, err := memory.ProjectScope(project)
	if err != nil {
		return base
	}
	own, _ := model.SplitReferenceContext(req.Text)
	// A Bot's memory belongs to its own project, wherever the conversation is.
	var bot memory.Scope
	if req.BotID != "" {
		if home, err := memory.ProjectScope(req.BotHome); err == nil {
			bot = memory.BotScope(home, req.BotID)
		}
	}
	p, err := mg.Store.PrepareBot(scope, bot, botOnly, session, fmt.Sprint(t.MemoryEpoch), own, TaskKey(t, session))
	if err != nil {
		return base
	}
	base.store, base.scope, base.prepared = mg.Store, scope, p
	return base
}

func (m Input) Text(raw string) string {
	raw += m.charter
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
