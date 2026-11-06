package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"miya/internal/codex"
)

// A window's controller mirrors the service: its signals hold the state the
// service pushes, and what would change the state is forwarded instead,
// guarded by c.remote at the top of each such method. Window state (the
// open conversation, the draft, the attachments) stays in the window.
type mirrorState struct {
	remoteHost  bool
	projectBusy map[string]bool
	messages    map[int][]Message
	// Draft edits belong to this window, including explicit clears after a
	// send. Older service snapshots must not replace them during navigation.
	drafts    map[int]string
	setActive func(int)
	setEffort func(string)
	setPolicy func(string)
	setLocale func(string)
	// reconnect connects again after the service went away; nil does not.
	reconnect func() (*serviceClient, svcState, error)
	// phone is the service's phone connection, for the settings.
	phone phoneStatus
}

// Service-backed windows never run agents themselves when a connection fails.
// Standalone operation is explicit, or used on platforms without the service.
func newWindowController() *chatController {
	store, err := loadDesktopWorkspaces()
	if err != nil {
		c := newMirrorController()
		c.remote = newClosedServiceClient()
		c.setStatus(err.Error())
		onCleanup(func() { c.cancel(); c.remote.Close() })
		return c
	}
	if link := store.current().Link; link != "" {
		c := newServiceWindow("", func() (*serviceClient, svcState, error) { return connectRemoteService(link) })
		c.mirror.remoteHost = true
		onCleanup(func() { c.cancel(); c.remote.Close() })
		return c
	}
	return newLocalWindowController()
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

func localWindowConnection() (*serviceClient, svcState, error) {
	workspace, explicit := localWindowWorkspace()
	cl, st, err := connectService(workspace)
	if err == nil && explicit != "" && st.Cwd != workspace {
		var result svcWorkspace
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

func newLocalWindowController() *chatController {
	if os.Getenv("MIYA_SERVICE") != "0" && runtime.GOOS != "windows" {
		workspace, _ := localWindowWorkspace()
		c := newServiceWindow(workspace, localWindowConnection)
		onCleanup(func() { c.cancel(); c.remote.Close() })
		return c
	}
	return newChatController()
}

func newServiceWindow(workspace string, connect func() (*serviceClient, svcState, error)) *chatController {
	c := newMirrorController()
	c.cwd = workspace
	c.mirror.reconnect = connect
	cl, st, err := connect()
	if err == nil {
		c.attachService(cl, st)
		return c
	}
	log.Printf("miya: waiting for background service: %v", err)
	// A closed transport keeps every action on the remote path. A nil remote
	// would make a mirror fall through to the local agent implementations.
	c.remote = newClosedServiceClient()
	c.setStatus("Waiting for background service: " + err.Error())
	c.watchService(c.remote)
	return c
}

func newMirrorController() *chatController {
	c := &chatController{mirror: &mirrorState{messages: map[int][]Message{}, drafts: map[int]string{}}, projectCache: map[string][]Thread{}}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.agent, c.setAgent = createSignal(agentBackends[0].ID())
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.projects, c.setProjects = createSignal([]Project{})
	c.threads, c.setThreads = createSignal([]Thread{})
	c.installNotifications()
	c.active, c.mirror.setActive = createSignal(0)
	c.setActive = func(id int) {
		if id != c.active() {
			c.mirror.setActive(id)
			c.forward("subscribe", id)
		}
	}
	c.draft, c.setDraft = createSignal("")
	c.model, c.setModel = createSignal("")
	c.effort, c.mirror.setEffort = createSignal("")
	c.setEffort = func(v string) { c.mirror.setEffort(v); c.forward("effort", v) }
	c.permissionPolicy, c.mirror.setPolicy = createSignal("on-request")
	c.setPermissionPolicy = func(v string) {
		v = normalizePermissionPolicy(v)
		c.mirror.setPolicy(v)
		c.forward("policy", v)
	}
	locale, setLocale := createSignal("en")
	c.locale = locale
	c.mirror.setLocale = func(v string) {
		v = normalizeLocale(v)
		setLocale(v)
	}
	c.setLocale = func(v string) { c.mirror.setLocale(v); c.forward("locale", normalizeLocale(v)) }
	c.models, c.setModels = createSignal([]codex.Model{})
	c.attachments, c.setAttachments = createSignal([]string{})
	c.status, c.setStatus = createSignal("")
	c.ready, c.setReady = createSignal(false)
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	c.installApprovalNotifications()
	c.historyStatus, c.setHistoryStatus = createSignal("")
	c.revision, c.setRevision = createSignal(0)
	c.onDraftReturned = c.restoreDraft
	return c
}

func (c *chatController) forward(op string, args any) { c.remote.Notify(op, args) }

// attachService starts mirroring a connected service.
func (c *chatController) attachService(cl *serviceClient, st svcState) {
	c.remote = cl
	c.desktopReadPending = nil
	c.mirror.messages = map[int][]Message{}
	c.applyState(st)
	// While a conversation streams the service pushes every few tens of
	// milliseconds; events that arrive before the main thread runs are
	// applied together, in one dispatch.
	inbox := &serviceInbox{}
	cl.SetEvents(func(m svcMessage) {
		if inbox.Push(m) {
			c.dispatch(func() {
				for _, m := range inbox.Take() {
					c.serviceEvent(cl, m)
				}
			})
		}
	})
	c.forward("subscribe", c.active())
	c.watchService(cl)
	c.startNotificationPresence(cl)
}

func (c *chatController) serviceEvent(cl *serviceClient, m svcMessage) {
	if c.remote != cl {
		return
	}
	switch {
	case m.State != nil:
		c.applyState(*m.State)
	case m.Messages != nil:
		c.applyMessages(*m.Messages)
	case m.Draft != nil:
		c.restoreDraft(m.Draft.Thread, m.Draft.Text, m.Draft.Attachments)
	}
}

// watchService reconnects after the service went away, as when it was
// replaced by a newer build; conversations it ran keep their state there.
func (c *chatController) watchService(cl *serviceClient) {
	reconnect := c.mirror.reconnect
	if reconnect == nil {
		return
	}
	go func() {
		select {
		case <-cl.Done():
		case <-c.ctx.Done():
			return
		}
		c.dispatch(func() {
			if c.ctx.Err() == nil && c.remote == cl && cl.HasTransport() {
				c.setReady(false)
				c.setStatus("Background service disconnected. Reconnecting…")
			}
		})
		for wait := 200 * time.Millisecond; ; wait = min(wait*2, 5*time.Second) {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-c.ctx.Done():
				timer.Stop()
				return
			}
			next, st, err := reconnect()
			if err != nil {
				continue
			}
			c.dispatch(func() {
				if c.ctx.Err() != nil {
					next.Close()
					return
				}
				c.attachService(next, st)
			})
			return
		}
	}()
}

func (c *chatController) applyState(s svcState) {
	active := c.active()
	hadActive := c.threadWithID(active).ID != 0
	c.collaborationRemote = s.Collaboration
	c.referenceSource = s.ReferenceSource
	c.memoryRevision = s.MemoryRevision
	c.fastMode = s.FastMode
	c.cwd = s.Cwd
	c.projectCache = s.ProjectThreads
	c.desktopUnread = s.DesktopUnread
	for key, version := range c.desktopReadPending {
		if s.DesktopUnread[key] != version {
			delete(c.desktopReadPending, key)
		}
	}
	c.mirror.projectBusy = s.ProjectBusy
	threads := append([]Thread(nil), s.Threads...)
	for i := range threads {
		if draft, ok := c.mirror.drafts[threads[i].ID]; ok {
			threads[i].Draft = draft
		}
		if m, ok := c.mirror.messages[threads[i].ID]; ok {
			threads[i].Messages = m
		}
	}
	if s.Approvals == nil {
		s.Approvals = []codex.Approval{}
	}
	c.setProjects(s.Projects)
	c.setThreads(threads)
	c.setApprovals(s.Approvals)
	c.setStatus(s.Status)
	c.setHistoryStatus(s.HistoryStatus)
	c.setReady(s.Ready)
	c.setModels(s.Models)
	c.setModel(s.Model)
	c.mirror.setEffort(s.Effort)
	c.mirror.setPolicy(s.PermissionPolicy)
	c.mirror.setLocale(s.Locale)
	c.claudeCommand, c.claudeError, c.claudeBinary, c.claudeArgs, c.claudeEnv = s.ClaudeCommand, s.ClaudeError, s.ClaudeBinary, s.ClaudeArgs, s.ClaudeEnv
	c.claudeModel, c.claudeEffort = s.ClaudeModel, s.ClaudeEffort
	c.claudeDefaultModel = s.ClaudeDefaultModel
	c.accountState, c.accountReset, c.quotaLimits, c.quotaStatus = s.Account, s.AccountReset, s.QuotaLimits, s.QuotaStatus
	c.mirror.phone = s.Phone
	// Each window owns its selection. When a conversation disappears from
	// the service, discard its stale draft/messages and select a live one.
	if hadActive && c.threadWithID(active).ID == 0 {
		delete(c.mirror.messages, active)
		delete(c.mirror.drafts, active)
		if len(threads) > 0 {
			c.selectThread(threads[0])
		} else {
			c.newThread()
		}
	}
	// No revision bump: a state carries no messages (applyMessages bumps it),
	// and the view's group cache keys on the thread, its busy flag and turn
	// itself. Bumping here regrouped the open conversation at every push.
}

func (c *chatController) applyMessages(m svcMessages) {
	if m.Thread != c.active() {
		return // for a conversation the window has left
	}
	known := map[int]Message{}
	if m.Reset {
		c.mirror.messages = map[int][]Message{}
	} else {
		for _, x := range c.mirror.messages[m.Thread] {
			known[x.ID] = x
		}
	}
	for _, x := range m.Changed {
		known[x.ID] = x
	}
	list := make([]Message, 0, len(m.Order))
	for _, id := range m.Order {
		if x, ok := known[id]; ok {
			list = append(list, x)
		}
	}
	c.mirror.messages[m.Thread] = list
	items := append([]Thread(nil), c.threads()...)
	for i := range items {
		if items[i].ID == m.Thread {
			items[i].Messages = list
		}
	}
	c.setThreads(items)
	c.setRevision(c.revision() + 1)
}

func (c *chatController) remoteWorkspace(path string) error {
	var res svcWorkspace
	if err := c.remote.Call("workspace", path, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	c.cwd = res.Cwd
	c.mirror.messages = map[int][]Message{}
	c.setActive(0)
	c.setDraft("")
	return nil
}

// mirroredHistory reports whether this window has received conversation
// id's messages from the service; a controller that is no mirror has them.
func (c *chatController) mirroredHistory(id int) bool {
	if c.mirror == nil {
		return true
	}
	_, received := c.mirror.messages[id]
	return received
}

// phoneControls are the phone connection the settings show and the action
// that changes it: this Mac's bridge, or a remote server's through its
// service. ok is false where neither applies.
func (c *chatController) phoneControls() (st phoneStatus, act func(string), ok bool) {
	switch {
	case desktopPhone != nil && !c.remoteHost():
		return desktopPhone.Status(), desktopPhone.Do, true
	case c.remote != nil && c.mirror != nil:
		return c.mirror.phone, func(a string) { c.forward("phone", a) }, true
	}
	return phoneStatus{}, nil, false
}
