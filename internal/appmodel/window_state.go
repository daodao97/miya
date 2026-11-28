package appmodel

import (
	"context"
	"miya/internal/translate"

	"miya/internal/agent/account"
	"miya/internal/chat/model"
	"miya/internal/interaction"
	projectmodel "miya/internal/project/model"
	svcclient "miya/internal/service/client"
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
	// TranslationRevision counts the replies translated where the agents
	// run: a window asks for those it shows untranslated as it changes.
	TranslationRevision uint64
	// Translation is how replies translate, kept where the agents run and
	// shared by windows and phones; Translating are the keys
	// (translate.Key) of the translations under way there.
	Translation     translate.Settings
	Translating     []string
	Model           func() string
	ProjectCache    map[string][]model.Thread
	Projects        func() []projectmodel.Project
	Ready           func() bool
	ReferenceSource string
	// Remote is the mirrored service, nil for a controller running agents locally.
	Remote              *svcclient.Client
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
