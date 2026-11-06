package appmodel

import (
	"context"

	"miya/internal/account"
	"miya/internal/interaction"
	"miya/internal/model"
	"miya/internal/svcproto"
)

// WindowState is the observable state of one window. Mutations run on its executor.
type WindowState struct {
	AccountReset account.ResetAction
	AccountState account.Display
	Active       func() int
	Agent        func() string
	Approvals    func() []interaction.Request
	Attachments  func() []string
	Cancel       context.CancelFunc
	// CommandsChanged refreshes completions when project commands arrive.
	CommandsChanged func()
	Ctx             context.Context
	Cwd             string
	DesktopUnread   map[string]int64
	Draft           func() string
	Effort          func() string
	HistoryStatus   func() string
	LastSubmitError string
	MemoryRevision  uint64
	Model           func() string
	ProjectCache    map[string][]model.Thread
	Projects        func() []model.Project
	Ready           func() bool
	ReferenceSource string
	// Remote is the mirrored service, nil for a controller running agents locally.
	Remote              *svcproto.Client
	RevealWorkspace     func()
	Revision            func() int
	SetActive           func(int)
	SetAgent            func(string)
	SetHistoryStatus    func(string)
	SetPermissionPolicy func(string)
	SetStatus           func(string)
	Threads             func() []model.Thread
	WorkspaceVisible    func() bool
}
