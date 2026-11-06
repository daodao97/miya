package controller

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"miya/internal/appmodel"
	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/prefs"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
	"miya/internal/workspaces"
)

// A window's controller mirrors the service: its signals hold the state the
// service pushes, and what would change the state is forwarded instead,
// guarded by c.remote at the top of each such method. Window state (the
// open conversation, the draft, the attachments) stays in the window.
type mirrorState struct {
	RemoteHost  bool
	projectBusy map[string]bool
	Messages    map[int][]model.Message
	// Draft edits belong to this window, including explicit clears after a
	// send. Older service snapshots must not replace them during navigation.
	Drafts    map[int]string
	setActive func(int)
	setEffort func(string)
	setPolicy func(string)
	SetLocale func(string)
	// reconnect connects again after the service went away; nil does not.
	Reconnect func() (*svcproto.Client, svcproto.State, error)
	// phone is the service's phone connection, for the settings.
	phone svcproto.PhoneStatus
}

// Service-backed windows never run agents themselves when a connection fails.
// Standalone operation is explicit, or used on platforms without the service.
func NewWindowController() *Controller {
	store, err := workspaces.Load()
	if err != nil {
		c := NewMirrorController()
		c.Remote = svcproto.NewClosedClient()
		c.SetStatus(err.Error())
		uiruntime.OnCleanup(func() { c.Cancel(); c.Remote.Close() })
		return c
	}
	if link := store.Current().Link; link != "" {
		c := NewServiceWindow("", func() (*svcproto.Client, svcproto.State, error) { return workspaces.ConnectRemoteService(link) })
		c.Mirror.RemoteHost = true
		uiruntime.OnCleanup(func() { c.Cancel(); c.Remote.Close() })
		return c
	}
	return NewLocalWindowController()
}

func localWindowWorkspace() (workspace, explicit string) {
	explicit = os.Getenv("QUICKGUI_WORKSPACE")
	workspace = explicit
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	workspace, _ = filepath.Abs(workspace)
	return
}

func LocalWindowConnection() (*svcproto.Client, svcproto.State, error) {
	workspace, explicit := localWindowWorkspace()
	cl, st, err := connectService(workspace)
	if err == nil && explicit != "" && st.Cwd != workspace {
		var result svcproto.WorkspaceResult
		if err = cl.Call("workspace", workspace, &result); err == nil && result.Error != "" {
			err = errors.New(result.Error)
		}
		if err == nil {
			err = cl.Call("state", nil, &st)
		}
		if err != nil {
			cl.Close()
		}
	}
	return cl, st, err
}

func NewLocalWindowController() *Controller {
	if os.Getenv("MIYA_SERVICE") != "0" && runtime.GOOS != "windows" {
		workspace, _ := localWindowWorkspace()
		c := NewServiceWindow(workspace, LocalWindowConnection)
		uiruntime.OnCleanup(func() { c.Cancel(); c.Remote.Close() })
		return c
	}
	return newChatController()
}

func NewServiceWindow(workspace string, connect func() (*svcproto.Client, svcproto.State, error)) *Controller {
	c := NewMirrorController()
	c.Cwd = workspace
	c.Mirror.Reconnect = connect
	cl, st, err := connect()
	if err == nil {
		c.AttachService(cl, st)
		return c
	}
	log.Printf("miya: waiting for background service: %v", err)
	// A closed transport keeps every action on the remote path. A nil remote
	// would make a mirror fall through to the local agent implementations.
	c.Remote = svcproto.NewClosedClient()
	c.SetStatus("Waiting for background service: " + err.Error())
	c.watchService(c.Remote)
	return c
}

func NewMirrorController() *Controller {
	c := &Controller{WindowState: appmodel.WindowState{ProjectCache: map[string][]model.Thread{}}, Mirror: &mirrorState{Messages: map[int][]model.Message{}, Drafts: map[int]string{}}}
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	c.Agent, c.SetAgent = uiruntime.CreateSignal(AgentBackends[0].ID())
	c.DraftPermission, c.SetDraftPermission = uiruntime.CreateSignal("")
	c.Projects, c.SetProjects = uiruntime.CreateSignal([]model.Project{})
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.InstallNotifications()
	c.Active, c.Mirror.setActive = uiruntime.CreateSignal(0)
	c.SetActive = func(id int) {
		if id != c.Active() {
			c.Mirror.setActive(id)
			c.forward("subscribe", id)
		}
	}
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Model, c.SetModel = uiruntime.CreateSignal("")
	c.Effort, c.Mirror.setEffort = uiruntime.CreateSignal("")
	c.SetEffort = func(v string) { c.Mirror.setEffort(v); c.forward("effort", v) }
	c.PermissionPolicy, c.Mirror.setPolicy = uiruntime.CreateSignal("on-request")
	c.SetPermissionPolicy = func(v string) {
		v = prefs.NormalizePermissionPolicy(v)
		c.Mirror.setPolicy(v)
		c.forward("policy", v)
	}
	locale, setLocale := uiruntime.CreateSignal("en")
	c.Locale = locale
	c.Mirror.SetLocale = func(v string) {
		v = prefs.NormalizeLocale(v)
		setLocale(v)
	}
	c.SetLocale = func(v string) { c.Mirror.SetLocale(v); c.forward("locale", prefs.NormalizeLocale(v)) }
	c.Models, c.SetModels = uiruntime.CreateSignal([]codex.Model{})
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	c.Ready, c.SetReady = uiruntime.CreateSignal(false)
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.installApprovalNotifications()
	c.HistoryStatus, c.SetHistoryStatus = uiruntime.CreateSignal("")
	c.Revision, c.SetRevision = uiruntime.CreateSignal(0)
	c.OnDraftReturned = c.RestoreDraft
	return c
}

func (c *Controller) forward(op string, args any) { c.Remote.Notify(op, args) }

// attachService starts mirroring a connected service.
func (c *Controller) AttachService(cl *svcproto.Client, st svcproto.State) {
	c.Remote = cl
	c.DesktopReadPending = nil
	c.Mirror.Messages = map[int][]model.Message{}
	c.ApplyState(st)
	// While a conversation streams the service pushes every few tens of
	// milliseconds; events that arrive before the main thread runs are
	// applied together, in one dispatch.
	inbox := &svcproto.Inbox{}
	cl.SetEvents(func(m svcproto.Envelope) {
		if inbox.Push(m) {
			c.Dispatch(func() {
				for _, m := range inbox.Take() {
					c.serviceEvent(cl, m)
				}
			})
		}
	})
	c.forward("subscribe", c.Active())
	c.watchService(cl)
	c.StartNotificationPresence(cl)
}

func (c *Controller) serviceEvent(cl *svcproto.Client, m svcproto.Envelope) {
	if c.Remote != cl {
		return
	}
	switch {
	case m.State != nil:
		c.ApplyState(*m.State)
	case m.Messages != nil:
		c.applyMessages(*m.Messages)
	case m.Draft != nil:
		c.RestoreDraft(m.Draft.Thread, m.Draft.Text, m.Draft.Attachments)
	}
}

// watchService reconnects after the service went away, as when it was
// replaced by a newer build; conversations it ran keep their state there.
func (c *Controller) watchService(cl *svcproto.Client) {
	reconnect := c.Mirror.Reconnect
	if reconnect == nil {
		return
	}
	go func() {
		select {
		case <-cl.Done():
		case <-c.Ctx.Done():
			return
		}
		c.Dispatch(func() {
			if c.Ctx.Err() == nil && c.Remote == cl && cl.HasTransport() {
				c.SetReady(false)
				c.SetStatus("Background service disconnected. Reconnecting…")
			}
		})
		for wait := 200 * time.Millisecond; ; wait = min(wait*2, 5*time.Second) {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-c.Ctx.Done():
				timer.Stop()
				return
			}
			next, st, err := reconnect()
			if err != nil {
				continue
			}
			c.Dispatch(func() {
				if c.Ctx.Err() != nil {
					next.Close()
					return
				}
				c.AttachService(next, st)
			})
			return
		}
	}()
}

func (c *Controller) ApplyState(s svcproto.State) {
	active := c.Active()
	hadActive := c.ThreadWithID(active).ID != 0
	c.CollaborationRemote = s.Collaboration
	c.ReferenceSource = s.ReferenceSource
	c.MemoryRevision = s.MemoryRevision
	c.fastMode = s.FastMode
	c.Cwd = s.Cwd
	c.ProjectCache = s.ProjectThreads
	c.DesktopUnread = s.DesktopUnread
	for key, version := range c.DesktopReadPending {
		if s.DesktopUnread[key] != version {
			delete(c.DesktopReadPending, key)
		}
	}
	c.Mirror.projectBusy = s.ProjectBusy
	threads := append([]model.Thread(nil), s.Threads...)
	for i := range threads {
		if draft, ok := c.Mirror.Drafts[threads[i].ID]; ok {
			threads[i].Draft = draft
		}
		if m, ok := c.Mirror.Messages[threads[i].ID]; ok {
			threads[i].Messages = m
		}
	}
	if s.Approvals == nil {
		s.Approvals = []codex.Approval{}
	}
	c.SetProjects(s.Projects)
	c.SetThreads(threads)
	c.SetApprovals(s.Approvals)
	c.SetStatus(s.Status)
	c.SetHistoryStatus(s.HistoryStatus)
	c.SetReady(s.Ready)
	c.SetModels(s.Models)
	c.SetModel(s.Model)
	c.Mirror.setEffort(s.Effort)
	c.Mirror.setPolicy(s.PermissionPolicy)
	c.Mirror.SetLocale(s.Locale)
	c.ClaudeCommand, c.ClaudeError, c.ClaudeBinary, c.ClaudeArgs, c.ClaudeEnv = s.ClaudeCommand, s.ClaudeError, s.ClaudeBinary, s.ClaudeArgs, s.ClaudeEnv
	c.ClaudeModel, c.ClaudeEffort = s.ClaudeModel, s.ClaudeEffort
	c.claudeDefaultModel = s.ClaudeDefaultModel
	c.AccountState, c.AccountReset, c.QuotaLimits, c.quotaStatus = s.Account, s.AccountReset, s.QuotaLimits, s.QuotaStatus
	c.Mirror.phone = s.Phone
	// Each window owns its selection. When a conversation disappears from
	// the service, discard its stale draft/messages and select a live one.
	if hadActive && c.ThreadWithID(active).ID == 0 {
		delete(c.Mirror.Messages, active)
		delete(c.Mirror.Drafts, active)
		if len(threads) > 0 {
			c.SelectThread(threads[0])
		} else {
			c.NewThread()
		}
	}
	// No revision bump: a state carries no messages (applyMessages bumps it),
	// and the view's group cache keys on the thread, its busy flag and turn
	// itself. Bumping here regrouped the open conversation at every push.
}

func (c *Controller) applyMessages(m svcproto.ThreadMessages) {
	if m.Thread != c.Active() {
		return // for a conversation the window has left
	}
	known := map[int]model.Message{}
	if m.Reset {
		c.Mirror.Messages = map[int][]model.Message{}
	} else {
		for _, x := range c.Mirror.Messages[m.Thread] {
			known[x.ID] = x
		}
	}
	for _, x := range m.Changed {
		known[x.ID] = x
	}
	list := make([]model.Message, 0, len(m.Order))
	for _, id := range m.Order {
		if x, ok := known[id]; ok {
			list = append(list, x)
		}
	}
	c.Mirror.Messages[m.Thread] = list
	items := append([]model.Thread(nil), c.Threads()...)
	for i := range items {
		if items[i].ID == m.Thread {
			items[i].Messages = list
		}
	}
	c.SetThreads(items)
	c.SetRevision(c.Revision() + 1)
}

func (c *Controller) remoteWorkspace(path string) error {
	var res svcproto.WorkspaceResult
	if err := c.Remote.Call("workspace", path, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	c.Cwd = res.Cwd
	c.Mirror.Messages = map[int][]model.Message{}
	c.SetActive(0)
	c.SetDraft("")
	return nil
}

// mirroredHistory reports whether this window has received conversation
// id's messages from the service; a controller that is no mirror has them.
func (c *Controller) MirroredHistory(id int) bool {
	if c.Mirror == nil {
		return true
	}
	_, received := c.Mirror.Messages[id]
	return received
}

// phoneControls are the phone connection the settings show and the action
// that changes it: this Mac's bridge, or a remote server's through its
// service. ok is false where neither applies.
func (c *Controller) PhoneControls() (st svcproto.PhoneStatus, act func(string), ok bool) {
	switch {
	case DesktopPhone != nil && !c.RemoteHost():
		return DesktopPhone.Status(), DesktopPhone.Do, true
	case c.Remote != nil && c.Mirror != nil:
		return c.Mirror.phone, func(a string) { c.forward("phone", a) }, true
	}
	return svcproto.PhoneStatus{}, nil, false
}
