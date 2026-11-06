package appmodel

import (
	"context"
	"miya/internal/codex"
)

// WindowState is the observable state of one window. Mutations run on its executor.
type WindowState struct {
	AccountReset        AccountResetAction
	AccountState        AccountDisplay
	Active              func() int
	Agent               func() string
	Approvals           func() []codex.Approval
	Attachments         func() []string
	Cancel              context.CancelFunc
	CommandsChanged     func()
	Ctx                 context.Context
	Cwd                 string
	DesktopUnread       map[string]int64
	Draft               func() string
	Effort              func() string
	HistoryStatus       func() string
	LastSubmitError     string
	MemoryRevision      uint64
	Model               func() string
	ProjectCache        map[string][]Thread
	Projects            func() []Project
	Ready               func() bool
	ReferenceSource     string
	Remote              *ServiceClient
	RevealWorkspace     func()
	Revision            func() int
	SetActive           func(int)
	SetAgent            func(string)
	SetHistoryStatus    func(string)
	SetPermissionPolicy func(string)
	SetStatus           func(string)
	Threads             func() []Thread
	WorkspaceVisible    func() bool
}
