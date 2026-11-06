package desktopui

import (
	"context"

	"miya/internal/agent"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/codex"
	"miya/internal/conversation"
	"miya/internal/memory/manager"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/runtime/tracker"
	"miya/internal/svcproto"
)

// Host is the controller contract for one desktop workspace. UI state stays in App.
type Host interface {
	conversation.Host
	State() *appmodel.WindowState
	AddAttachments(paths []string)
	AgentSettings() []agent.Setting
	AgentSpeedBadge(agent string) string
	AllThreads() []model.Thread
	ApprovalDetail(a codex.Approval) string
	ArchiveProject(path string)
	ArchiveThread(t model.Thread)
	AskAccountReset()
	AttachService(cl *svcproto.Client, st svcproto.State)
	AutomationOp(op string, r automation.Request) (any, error)
	BackendReady() bool
	CanRewind(t model.Thread, m model.Message) bool
	CancelAccountReset()
	ChangeWorkspace(path string) error
	CollaborationCapabilities() *model.CollaborationStatus
	CollaborationUserOp(r appmodel.CollaborationRequest) (any, error)
	ConfigureMirror(remote bool, reconnect func() (*svcproto.Client, svcproto.State, error))
	ConfirmAccountReset()
	Connect()
	Current() model.Thread
	CurrentAgent() string
	CurrentAgentControls() agent.Controls
	CurrentPermissionPolicy() string
	DeleteProject(path string)
	DeleteThread(t model.Thread)
	DesktopThreadUnread(t model.Thread) bool
	Dispatch(f func())
	EnsurePhoneHost()
	Memories() *manager.Manager
	MirroredHistory(id int) bool
	NewThread()
	PhoneControls() (st svcproto.PhoneStatus, act func(string), ok bool)
	PrefetchReference(ref model.ThreadRef)
	ProjectBusy(path string) bool
	ReadDesktopThread(t model.Thread)
	ReadRuntime(ctx context.Context, ref model.ThreadRef, project string) (tracker.ConversationRuntime, error)
	RefreshAccount()
	RememberDraft(value string)
	RemoteHost() bool
	RemoveAttachment(path string)
	RemoveQueued(threadID int, queuedID int)
	RenameProject(path string, name string)
	Respond(a codex.Approval, result any)
	RespondMCPAlways(a codex.Approval) error
	Resume(t model.Thread, after *model.SendRequest)
	ResumeQueue(id int)
	RetryResponse(req appmodel.RetryRequest) error
	Rewind(threadID int, messageID int, text string)
	RunThreadCommand(command string) string
	Runtimes() *tracker.Manager
	SavedReferenceTitle(ref model.ThreadRef) (string, bool)
	SelectThread(t model.Thread)
	SelectedPermissionPolicy() string
	SendDraft(steer bool) int
	SetCurrentPermissionPolicy(policy string)
	SteerQueued(threadID int, queuedID int)
	Stop()
	TaskAction(threadID int, taskID string, action string) string
	TaskWaitingApproval(t model.Thread) bool
	ThreadRef(t model.Thread) model.ThreadRef
	ThreadRefProjects() []phoneapi.Project
	ThreadWithID(id int) model.Thread
	ThreadWorkspace(id int) string
	WorkspaceFiles() appmodel.WorkspaceFiles
}

// Environment supplies controller creation at the executable's composition root.
type Environment struct {
	NewWindow       func() Host
	NewLocal        func() Host
	NewMirror       func() Host
	LocalConnection func() (*svcproto.Client, svcproto.State, error)
}

var environment Environment

func ConfigureEnvironment(e Environment) { environment = e }
