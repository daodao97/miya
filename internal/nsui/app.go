package nsui

import (
	"sync"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"miya/internal/agent"
	"miya/internal/agent/cliupdate"
	"miya/internal/agent/login"
	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/memory/manager"
	"miya/internal/nswidgets"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
)

// Host is what the NSGo desktop needs of a window's controller. It grows
// toward internal/ui.Host as views move over; the controller already
// implements the whole of that.
type Host interface {
	requestsHost
	AddAttachments(paths []string)
	RemoveAttachment(path string)
	BackendReady() bool
	CurrentAgent() string
	CurrentAgentControls() agent.Controls
	AgentSpeedBadge(agent string) string
	CurrentPermissionPolicy() string
	SetCurrentPermissionPolicy(policy string)
	Resume(t model.Thread, after *model.SendRequest)
	RemoveQueued(threadID int, queuedID int)
	ResumeQueue(id int)
	SteerQueued(threadID int, queuedID int)
	CanRewind(t model.Thread, m model.Message) bool
	Rewind(threadID int, messageID int, text string)
	PrefetchReference(ref model.ThreadRef)
	ThreadRefProjects() []model.ProjectSnapshot
	SavedReferenceTitle(ref model.ThreadRef) (string, bool)
	RunThreadCommand(command string) string
	TaskAction(threadID int, taskID string, action string) string
	TaskWaitingApproval(t model.Thread) bool
	CollaborationCapabilities() *model.CollaborationStatus
	CollaborationUserOp(r appmodel.CollaborationRequest) (any, error)
	AutomationOp(op string, r automation.Request) (any, error)
	EnabledAgents() []string
	ThreadWithID(id int) model.Thread
	State() *appmodel.WindowState
	Current() model.Thread
	SelectThread(t model.Thread)
	RetryResponse(req appmodel.RetryRequest) error
	RememberDraft(value string)
	SendDraft(steer bool) int
	Stop()
	NewThread()
	EnsurePhoneHost()
	AgentSettings() []agent.Setting
	Agents() []agent.Availability
	SetAgentEnabled(id string, on bool)
	RecheckAgents()
	EnsureAgentUpdates()
	AgentUpdates() map[string]cliupdate.Info
	AgentUpdateBusy() bool
	UpgradeAgent(string)
	SelectedPermissionPolicy() string
	RefreshAccount()
	AccountLoginTarget() func(login.Request) (login.State, error)
	RemoteHost() bool
	AskAccountReset()
	CancelAccountReset()
	ConfirmAccountReset()
	PhoneControls() (st svcproto.PhoneStatus, act func(string), ok bool)
	AllThreads() []model.Thread
	ThreadRef(model.Thread) model.ThreadRef
	ThreadWorkspace(id int) string
	ChangeWorkspace(path string) error
	Memories() *manager.Manager
}

// Environment supplies controller creation at the executable's composition
// root, as internal/ui.Environment does.
type Environment struct {
	NewWindow func() Host
}

var environment Environment

func ConfigureEnvironment(e Environment) { environment = e }

// DesktopMain runs the desktop app on NSGo until it quits.
func DesktopMain() {
	nsgo.Run(appMain,
		nsgo.WindowSize(1180, 820),
		nsgo.WindowConstraints(core.WindowConstraints{MinWidth: 780, MinHeight: 580}),
	)
}

// appMain runs once in the app's scope: it bridges the controller to the
// engine, then opens the main window.
func appMain(cx *core.Scope) {
	b := installBridge(cx)
	cx.SetTheme(nswidgets.Theme())
	installPlatformServices(cx)
	sampleIdle(cx)
	c := environment.NewWindow()
	// With the background service running, the service serves phones: a
	// window only mirrors the current project.
	c.EnsurePhoneHost()
	nsgo.SetMainView(cx, func(cx *core.Scope) core.Node { return root(cx, c) })
	b.setWindow(desktop.MainWindow(cx))
}

// installPlatformServices gives the controller NSGo's notifications. Idle
// time is left unset, so the user counts as present, until Miya reads it
// from the system itself.
func installPlatformServices(cx *core.Scope) {
	var mu sync.Mutex
	clicks := map[string]func(){}
	cx.OnNotificationPress(func(id string) {
		mu.Lock()
		f := clicks[id]
		delete(clicks, id)
		mu.Unlock()
		if f != nil {
			f()
		}
	})
	uiruntime.ShowNotice = func(n uiruntime.Notice) error {
		if n.OnClick != nil {
			mu.Lock()
			clicks[n.ID] = n.OnClick
			mu.Unlock()
		}
		return cx.ShowNotification(core.Notification{ID: n.ID, Title: n.Title, Body: n.Body}, nil)
	}
}
