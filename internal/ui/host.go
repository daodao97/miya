package desktopui

import (
	"context"

	"miya/internal/agent"
	"miya/internal/agent/cliupdate"
	"miya/internal/agent/login"
	"miya/internal/agent/provider"
	"miya/internal/agent/quota"
	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
	"miya/internal/interaction"
	"miya/internal/memory/manager"
	projectaccess "miya/internal/project/access"
	"miya/internal/runtime/tracker"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/translate"
)

// Host is the controller contract for one desktop workspace. UI state stays in App.
type Host interface {
	AgentModels(string) []agent.ModelOption
	conversation.Host
	State() *appmodel.WindowState
	AddAttachments(paths []string)
	AgentSettings() []agent.Setting
	Agents() []agent.Availability
	EnabledAgents() []string
	SetAgentEnabled(id string, on bool)
	RecheckAgents()
	EnsureAgentUpdates()
	AgentUpdates() map[string]cliupdate.Info
	AgentUpdateBusy() bool
	UpgradeAgent(string)
	AgentEnvironment() cliupdate.Environment
	SetAgentEnvironment(string)
	// Model providers configured in Miya, and the one each agent uses.
	ProviderViews() []provider.View
	ProviderUse() map[string]string
	ProviderError() string
	SaveProvider(provider.Edit) error
	DeleteProvider(id string) error
	EnableProvider(agent, id string, on bool) error
	ProviderModelsFetch(provider.Edit) func(context.Context) ([]string, error)
	AgentSpeedBadge(agent string) string
	AllThreads() []model.Thread
	ApprovalDetail(a interaction.Request) string
	DescribeApproval(a interaction.Request) appmodel.ApprovalView
	ArchiveProject(path string)
	ArchiveThread(t model.Thread)
	AskAccountReset()
	AttachService(cl *svcclient.Client, st svcproto.State)
	AutomationOp(op string, r automation.Request) (any, error)
	// Translate puts a reply into the language r.Lang with an agent.
	// It waits for the agent: call it off the main thread.
	Translate(r translate.Request) (string, error)
	// TranslationLookup returns the translations kept of keys
	// (translate.Key), off the main thread.
	TranslationLookup(keys []string) (map[string]string, error)
	// SetTranslationSettings keeps a change of how replies translate where
	// the agents run, on the main thread.
	SetTranslationSettings(s translate.Settings)
	BackendReady() bool
	// Bots are the named assistants. BotThread is a Bot's standing
	// conversation among the loaded ones; CurrentBot is the Bot whose
	// conversation is open, or the one the empty conversation is for.
	Bots() []bot.Bot
	BotOp(op string, r bot.Request) (bot.Bot, error)
	BotThread(b bot.Bot) model.Thread
	// BotReportThread is the conversation of a Bot's report, if loaded.
	BotReportThread(r bot.Report) model.Thread
	CurrentBot() (bot.Bot, bool)
	SetDraftBot(id string)
	CanRewind(t model.Thread, m model.Message) bool
	CancelAccountReset()
	ChangeWorkspace(path string) error
	CollaborationCapabilities() *model.CollaborationStatus
	CollaborationUserOp(r appmodel.CollaborationRequest) (any, error)
	ConfigureMirror(remote bool, reconnect func() (*svcclient.Client, svcproto.State, error))
	ConfirmAccountReset()
	Connect()
	Current() model.Thread
	CurrentAgent() string
	CurrentAgentControls() agent.Controls
	CurrentPermissionPolicy() string
	DeleteProject(path string)
	DeleteThread(t model.Thread)
	DesktopThreadUnread(t model.Thread) bool
	WaitingThreads() appmodel.Waiting
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
	AccountLoginTarget() func(login.Request) (login.State, error)
	RememberDraft(value string)
	RemoteHost() bool
	RemoveAttachment(path string)
	RemoveQueued(threadID int, queuedID int)
	RenameProject(path string, name string)
	Answer(a interaction.Request, reply interaction.Response)
	RespondMCPAlways(a interaction.Request) error
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
	ThreadRefProjects() []model.ProjectSnapshot
	ThreadWithID(id int) model.Thread
	ThreadWorkspace(id int) string
	WorkspaceFiles() projectaccess.Files
}

// Environment supplies controller creation at the executable's composition root.
type Environment struct {
	Quotas          func() *quota.Service
	NewWindow       func() Host
	NewLocal        func() Host
	NewMirror       func() Host
	LocalConnection func() (*svcclient.Client, svcproto.State, error)
}

var environment Environment

func ConfigureEnvironment(e Environment) { environment = e }

func quotaService() *quota.Service {
	if environment.Quotas != nil {
		return environment.Quotas()
	}
	return quota.NewService()
}
