// Package manager is the memory feature of the background service: it
// follows conversation rounds for extraction, adds memories to the messages
// agents receive, and answers what windows, phones and agents ask of the
// memory library.
package manager

import (
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
)

// Host is what the manager needs from the controller that owns the
// conversations.
type Host interface {
	// Dispatch runs f on the controller's thread.
	Dispatch(f func())
	Thread(id int) model.Thread
	UpdateThread(id int, f func(*model.Thread))
	ThreadRef(model.Thread) model.ThreadRef
	// Workspace is the project directory a conversation runs in.
	Workspace(thread int) string
	// NextID is a new message ID.
	NextID() int
	// Projects is the open project and the others the controller knows.
	Projects() (cwd string, paths []string)
	// ClaudeBinary is the Claude Code executable, "" when none is found.
	ClaudeBinary() string
	// Changed tells windows the memory library changed.
	Changed()
}

// Manager holds the memory state of the controller.
type Manager struct {
	Store   *memory.Store // non-nil only in the background service
	Calls   int           // executor-owned; pending IO prevents idle service exit
	Engine  *extractor.Engine
	Turns   map[int]Turn
	results map[string]bool
	Host    Host
}
