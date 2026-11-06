package desktopui

import (
	"context"
	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/codex"
	"miya/internal/conversation"
	"miya/internal/memory/manager"
)

// Host is the controller contract for one desktop workspace. UI state stays in App.
type Host interface {
	conversation.Host
	State() *appmodel.WindowState
	AddAttachments(paths []string)
	AgentSettings() []appmodel.SettingControls
	AgentSpeedBadge(agent string) string
	AllThreads() []appmodel.Thread
	ApprovalDetail(a codex.Approval) string
	ArchiveProject(path string)
	ArchiveThread(t appmodel.Thread)
	AskAccountReset()
	AttachService(cl *appmodel.ServiceClient, st appmodel.SvcState)
	AutomationOp(op string, r automation.Request) (any, error)
	BackendReady() bool
	CanRewind(t appmodel.Thread, m appmodel.Message) bool
	CancelAccountReset()
	ChangeWorkspace(path string) error
	ClaudeCommandsFor(cwd string) ([]appmodel.ComposerChoice, bool)
	CollaborationCapabilities() *appmodel.CollaborationStatus
	CollaborationUserOp(r appmodel.CollaborationRequest) (any, error)
	ConfigureMirror(remote bool, reconnect func() (*appmodel.ServiceClient, appmodel.SvcState, error))
	ConfirmAccountReset()
	Connect()
	Current() appmodel.Thread
	CurrentAgent() string
	CurrentAgentControls() appmodel.AgentControls
	CurrentPermissionPolicy() string
	DeleteProject(path string)
	DeleteThread(t appmodel.Thread)
	DesktopThreadUnread(t appmodel.Thread) bool
	Dispatch(f func())
	EnsurePhoneHost()
	KnownClaudeCommands(cwd string) []appmodel.ComposerChoice
	Memories() *manager.Manager
	MirroredHistory(id int) bool
	NewThread()
	PhoneControls() (st appmodel.PhoneStatus, act func(string), ok bool)
	PrefetchReference(ref appmodel.ThreadRef)
	ProjectBusy(path string) bool
	ReadDesktopThread(t appmodel.Thread)
	ReadRuntime(ctx context.Context, ref appmodel.ThreadRef, project string) (appmodel.ConversationRuntime, error)
	RefreshAccount()
	RememberDraft(value string)
	RemoteHost() bool
	RemoveAttachment(path string)
	RemoveQueued(threadID int, queuedID int)
	RenameProject(path string, name string)
	Respond(a codex.Approval, result any)
	RespondMCPAlways(a codex.Approval) error
	Resume(t appmodel.Thread, after *appmodel.SendRequest)
	ResumeQueue(id int)
	RetryResponse(req appmodel.RetryRequest) error
	Rewind(threadID int, messageID int, text string)
	RunThreadCommand(command string) string
	Runtimes() *appmodel.RuntimeManager
	SavedReferenceTitle(ref appmodel.ThreadRef) (string, bool)
	SelectThread(t appmodel.Thread)
	SelectedPermissionPolicy() string
	SendDraft(steer bool) int
	SetCurrentPermissionPolicy(policy string)
	SteerQueued(threadID int, queuedID int)
	Stop()
	TaskAction(threadID int, taskID string, action string) string
	TaskWaitingApproval(t appmodel.Thread) bool
	ThreadRef(t appmodel.Thread) appmodel.ThreadRef
	ThreadRefProjects() []appmodel.PhoneProject
	ThreadWithID(id int) appmodel.Thread
	ThreadWorkspace(id int) string
	WorkspaceFiles() appmodel.WorkspaceFiles
}

// Environment supplies controller creation at the executable's composition root.
type Environment struct {
	NewWindow       func() Host
	NewLocal        func() Host
	NewMirror       func() Host
	LocalConnection func() (*appmodel.ServiceClient, appmodel.SvcState, error)
}

var environment Environment

func ConfigureEnvironment(e Environment) { environment = e }
