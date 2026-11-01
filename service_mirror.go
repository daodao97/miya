package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"miya/internal/codex"
)

// A window's controller mirrors the service: its signals hold the state the
// service pushes, and what would change the state is forwarded instead,
// guarded by c.remote at the top of each such method. Window state (the
// open conversation, the draft, the attachments) stays in the window.
type mirrorState struct {
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

// newWindowController connects to the service, starting it if needed, and
// runs everything in the window's process when that fails.
func newWindowController() *chatController {
	if os.Getenv("MIYA_SERVICE") != "0" {
		explicit := os.Getenv("QUICKGUI_WORKSPACE")
		workspace := explicit
		if workspace == "" {
			workspace, _ = os.Getwd()
		}
		workspace, _ = filepath.Abs(workspace)
		cl, st, err := connectService(workspace)
		if err == nil {
			c := newMirrorController()
			c.mirror.reconnect = func() (*serviceClient, svcState, error) { return connectService(workspace) }
			c.attachService(cl, st)
			if explicit != "" && st.Cwd != workspace {
				_ = c.changeWorkspace(workspace)
			}
			return c
		}
		log.Printf("miya: background service unavailable, running in this process: %v", err)
	}
	return newChatController()
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
	c.mirror.setLocale = func(v string) { appLocale.Store(normalizeLocale(v)); setLocale(normalizeLocale(v)) }
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

func (c *chatController) forward(op string, args any) { c.remote.notify(op, args) }

// attachService starts mirroring a connected service.
func (c *chatController) attachService(cl *serviceClient, st svcState) {
	c.remote = cl
	c.mirror.messages = map[int][]Message{}
	c.applyState(st)
	cl.setEvents(func(m svcMessage) { c.dispatch(func() { c.serviceEvent(cl, m) }) })
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
		<-cl.closed
		c.dispatch(func() {
			if c.remote == cl {
				c.setReady(false)
				c.setStatus("后台服务已断开，正在重新连接…")
			}
		})
		for wait := 200 * time.Millisecond; ; wait = min(wait*2, 5*time.Second) {
			time.Sleep(wait)
			next, st, err := reconnect()
			if err != nil {
				continue
			}
			c.dispatch(func() { c.attachService(next, st) })
			return
		}
	}()
}

func (c *chatController) applyState(s svcState) {
	c.fastMode = s.FastMode
	c.cwd = s.Cwd
	c.projectCache = s.ProjectThreads
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
	c.setRevision(c.revision() + 1)
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
	if err := c.remote.call("workspace", path, &res); err != nil {
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
