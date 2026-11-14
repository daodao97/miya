package nsui

import (
	"sync"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"miya/internal/agent"
	"miya/internal/agent/cliupdate"
	"miya/internal/agent/login"
	"miya/internal/agent/quota"
	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/memory/manager"
	"miya/internal/nswidgets"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/service/workspaces"
	"miya/internal/uiruntime"
)

// Host is what the NSGo desktop needs of a window's controller. It grows
// toward internal/ui.Host as views move over; the controller already
// implements the whole of that.
type Host interface {
	requestsHost
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
	Quotas    func() *quota.Service
	NewWindow func() Host
	// NewLocal and NewMirror make the controllers of other workspaces:
	// one running conversations itself, one mirroring a service.
	NewLocal        func() Host
	NewMirror       func() MirrorHost
	LocalConnection func() (*svcclient.Client, svcproto.State, error)
}

// MirrorHost is a controller mirroring a background service.
type MirrorHost interface {
	Host
	ConfigureMirror(remote bool, reconnect func() (*svcclient.Client, svcproto.State, error))
	AttachService(cl *svcclient.Client, st svcproto.State)
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
	store, err := workspaces.Load()
	hub := newWorkspaceHub(cx, c, store, err)
	// Switching workspace shows that workspace's controller in a new shell.
	nsgo.SetMainView(cx, func(cx *core.Scope) core.Node {
		return core.Dynamic(func(cx *core.Scope) core.Node {
			h := hub.active.Get()
			usageKey.Provide(cx, hub.usage)
			return root(cx, h, workspaceSelector(cx, hub))
		})
	})
	w := desktop.MainWindow(cx)
	b.setWindow(w)
	// Keep the app-scoped usage tray and controllers alive when the window closes.
	w.OnClosing(cx, func(close *desktop.WindowClosing) { close.Cancel(); w.Hide() })
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
