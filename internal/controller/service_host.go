package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/agent/login"
	agentpolicy "miya/internal/agent/policy"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/chat/model"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/notification"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	svcproto "miya/internal/service/protocol"
	serviceserver "miya/internal/service/server"
	"miya/internal/uiruntime"
)

func NewService(c *Controller, ex *async.Executor) *serviceserver.Service {
	c.runtimePath = filepath.Join(appdir.Dir(), "runtime.json")
	c.Memories().Store = memory.New(filepath.Join(appdir.Dir(), "memory"))
	s := serviceserver.New(c, ex)
	c.Push = notification.NewHub()
	c.Run = ex.Push
	c.OnDraftReturned = s.DraftReturned
	c.Memories().Engine = NewMemoryEngine(c)
	c.Collaboration = NewCollaboration(c, filepath.Join(appdir.Dir(), "delegations"))
	return s
}

func (c *Controller) ServiceRequest(req svcproto.Request) (any, error) {
	arg := func(v any) error {
		if len(req.Args) == 0 {
			return nil
		}
		return json.Unmarshal(req.Args, v)
	}
	if mcpproto.IsOperation(req.Op) {
		var request mcpproto.Request
		if err := arg(&request); err != nil {
			return nil, err
		}
		return c.MCP().Handle(req.Op, request)
	}
	var id int
	var value string
	switch req.Op {
	case "accountLogin":
		var r login.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		run := c.AccountLoginTarget()
		return async.Reply(func() (any, error) { return run(r) }), nil
	case "host":
		var r svcproto.HostRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.PrepareHost(r)
	case "commands":
		_ = arg(&value)
		list, known := c.ClaudeCommandsFor(value)
		return svcproto.HostCommands{Choices: list, Known: known}, nil
	case "notification-presence":
		var present bool
		if err := arg(&present); err != nil {
			return nil, err
		}
		if present && c.Push != nil {
			c.Push.NoteDesktopPresence(time.Now().Add(25 * time.Second))
		}
	case "runtimeValidate":
		var r webservice.Service
		if err := arg(&r); err != nil {
			return nil, err
		}
		manager := c.Runtimes()
		return async.Reply(func() (any, error) {
			ctx, cancel := context.WithTimeout(manager.Context(), 5*time.Second)
			defer cancel()
			return nil, manager.ValidateToken(ctx, r.RuntimeToken, r.Host, r.Port)
		}), nil
	case "runtime":
		var r tracker.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.PrepareRuntime(r.Ref, r.Project)
	case "desktop-read":
		var read appmodel.DesktopRead
		if err := arg(&read); err != nil {
			return nil, err
		}
		c.MarkDesktopRead(read)
	case "collaboration":
		var r appmodel.CollaborationRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.CollaborationUserOp(r)
	case "memoryList", "memoryGet", "memorySave", "memoryForget", "memoryDelete", "memoryConfigure", "memorySessionConfigure", "memoryPromoteUser", "memoryPromotionPreview", "memoryPromoteFile":
		var r manager.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.Memories().Op(req.Op, r)
	case "automationList", "automationDraft", "automationSave", "automationToggle", "automationDelete", "automationRunNow", "automationWorktree", "automationWake", "automationWebhook":
		var r automation.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.AutomationOp(req.Op, r)
	case "prefetch":
		var ref model.ThreadRef
		if err := arg(&ref); err != nil {
			return nil, err
		}
		c.PrefetchReference(ref)
	case "submit":
		var r model.SendRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		id := c.Submit(r)
		if id == 0 && c.LastSubmitError != "" {
			return nil, fmt.Errorf("%s", c.LastSubmitError)
		}
		return id, nil
	case "retry":
		var r appmodel.RetryRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return nil, c.RetryResponse(r)
	case "stop":
		_ = arg(&id)
		c.StopThread(id)
	case "archive", "delete", "open", "resume":
		_ = arg(&id)
		t := c.ThreadWithID(id)
		if t.ID == 0 {
			return nil, nil
		}
		switch req.Op {
		case "archive":
			c.ArchiveThread(t)
		case "delete":
			c.DeleteThread(t)
		case "open":
			c.BackendOf(t).Open(t)
		case "resume":
			c.Resume(t, nil)
		}
	case "respond":
		var a struct {
			ID       json.RawMessage `json:"id"`
			Result   json.RawMessage `json:"result"`
			Remember bool            `json:"remember,omitempty"`
		}
		_ = arg(&a)
		var result any
		_ = json.Unmarshal(a.Result, &result)
		for _, ap := range c.Approvals() {
			if string(ap.ID) == string(a.ID) {
				if a.Remember {
					return nil, c.RespondMCPAlways(ap)
				}
				c.Respond(ap, result)
				break
			}
		}
		if a.Remember {
			return nil, fmt.Errorf("Request already handled or expired")
		}
	case "rewind":
		var a svcproto.Rewind
		_ = arg(&a)
		c.Rewind(a.Thread, a.Message, a.Text)
	case "task":
		var a svcproto.TaskAction
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return nil, err
		}
		return c.TaskAction(a.Thread, a.Task, a.Action), nil
	case "command":
		var a svcproto.Command
		_ = arg(&a)
		thread, message := c.ThreadCommand(a.Thread, a.Agent, a.Command, a.Policy)
		return svcproto.CommandResult{Thread: thread, Message: message, FastMode: c.fastMode}, nil
	case "workspace":
		_ = arg(&value)
		if err := c.ChangeWorkspace(value); err != nil {
			return svcproto.WorkspaceResult{Error: err.Error()}, nil
		}
		return svcproto.WorkspaceResult{Cwd: c.Cwd}, nil
	case "connect":
		c.Connect()
	case "model", "effort", "policy", "locale", "claudeCommand":
		_ = arg(&value)
		switch req.Op {
		case "model":
			c.SelectModel(value)
		case "effort":
			c.setCodexEffort(value)
		case "policy":
			c.SetPermissionPolicy(value)
		case "locale":
			c.SetLocale(value)
		case "claudeCommand":
			c.SetClaudeCommand(value)
		}
	case "threadPolicy", "draft":
		var a svcproto.ThreadValue
		_ = arg(&a)
		if a.Thread != 0 {
			c.Update(a.Thread, func(t *model.Thread) {
				if req.Op == "draft" {
					t.Draft = a.Value
				} else {
					t.PermissionPolicy = agentpolicy.Normalize(a.Value)
				}
			})
		}
	case "renameProject", "archiveProject", "deleteProject":
		var a svcproto.ProjectRequest
		_ = arg(&a)
		switch req.Op {
		case "renameProject":
			c.RenameProject(a.Path, a.Name)
		case "archiveProject":
			c.ArchiveProject(a.Path)
		case "deleteProject":
			c.DeleteProject(a.Path)
		}
	case "phone":
		_ = arg(&value)
		DesktopPhone.Do(value)
		uiruntime.Invalidate()
	case "dequeue":
		var a svcproto.Queued
		_ = arg(&a)
		c.RemoveQueued(a.Thread, a.ID)
	case "steerQueued":
		var a svcproto.Queued
		_ = arg(&a)
		c.SteerQueued(a.Thread, a.ID)
	case "drainQueue":
		_ = arg(&id)
		c.ResumeQueue(id)
	case "claudeModel":
		_ = arg(&value)
		c.setClaudeModel(value)
	case "piCommand":
		_ = arg(&value)
		c.SetPiCommand(value)
	case "piModel":
		_ = arg(&value)
		c.setPiChoice("piModel", &c.Pi.Model, value)
	case "piEffort":
		_ = arg(&value)
		c.setPiChoice("piEffort", &c.Pi.Effort, value)
	case "agentEnabled":
		var a struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		_ = arg(&a)
		c.SetAgentEnabled(a.ID, a.Enabled)
	case "checkAgentUpdates":
		c.CheckAgentUpdates()
	case "upgradeAgent":
		_ = arg(&value)
		c.UpgradeAgent(value)
	case "recheckAgents":
		c.RecheckAgents()
	case "claudeEffort":
		_ = arg(&value)
		c.setClaudeEffort(value)
	case "account":
		c.RefreshAccount()
	case "accountAsk":
		c.AskAccountReset()
	case "accountCancel":
		c.CancelAccountReset()
	case "accountConfirm":
		c.ConfirmAccountReset()
	default:
		return nil, fmt.Errorf("unknown operation %q", req.Op)
	}
	return nil, nil
}

// busy reports a turn running or starting, which quitting would end.
func (c *Controller) ServiceBusy() bool {
	if c.AgentUpdateBusy() {
		return true
	}
	if c.logins != nil && c.logins.Busy() {
		return true
	}
	if c.Collaboration != nil && c.Collaboration.Context().Err() == nil && c.Collaboration.Pending() > 0 {
		return true
	}
	if c.RemoteTerminals.Load() > 0 {
		return true
	}
	if c.desktopBusy() {
		return true
	}
	if c.runtimeBusy() {
		return true
	}
	if c.Memories().Calls > 0 || c.Memories().Engine != nil && c.Memories().Engine.Busy() || len(c.ClaudeRuns) > 0 {
		return true
	}
	for _, t := range c.Threads() {
		if t.Busy || t.TaskPending || model.ActiveGoal(t) {
			return true
		}
	}
	for path, threads := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		for _, t := range threads {
			if t.Busy || t.TaskPending || model.ActiveGoal(t) {
				return true
			}
		}
	}
	return false
}

func (c *Controller) ServiceStop() {
	if c.logins != nil {
		c.logins.Close()
	}
	if c.Collaboration != nil {
		c.Collaboration.Stop()
	}
	if c.Memories().Engine != nil {
		c.Memories().Engine.Stop()
	}
}
func (c *Controller) ServicePause() {
	if c.Memories().Engine != nil {
		c.Memories().Engine.Pause()
	}
}
func (c *Controller) ServiceKeepAlive() bool { return DesktopPhone.Connected() || c.Auto.Enabled() }
func (c *Controller) SnapshotState() svcproto.State {
	busy := make(map[string]bool)
	for _, p := range c.Projects() {
		if c.ProjectBusy(p.Path) {
			busy[p.Path] = true
		}
	}
	threads := make([]model.Thread, len(c.Threads()))
	for i, t := range c.Threads() {
		draft := t.Draft
		t = model.ListThread(t)
		t.Draft = draft
		threads[i] = t
	}
	projectThreads := make(map[string][]model.Thread, len(c.ProjectCache))
	for path, cached := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		items := make([]model.Thread, len(cached))
		for i, t := range cached {
			t = model.ListThread(t)
			t.Queue = nil
			items[i] = t
		}
		projectThreads[path] = items
	}
	return svcproto.State{
		Collaboration:   c.CollaborationCapabilities(),
		ReferenceSource: c.ReferenceSource,
		MemoryRevision:  c.MemoryRevision,
		FastMode:        c.fastMode,
		ProjectBusy:     busy,
		ProjectThreads:  projectThreads,
		DesktopUnread:   c.DesktopUnread,
		Cwd:             c.Cwd, Projects: c.Projects(), Threads: threads, Approvals: c.Approvals(),
		Status: c.Status(), HistoryStatus: c.HistoryStatus(), Ready: c.Ready(),
		Models: snapshotModels(c.Models()), Model: c.Model(), Effort: c.Effort(), PermissionPolicy: c.PermissionPolicy(), Locale: c.Locale(),
		ClaudeCommand: c.ClaudeCommand, ClaudeError: c.ClaudeError, ClaudeBinary: c.ClaudeBinary, ClaudeArgs: c.ClaudeArgs, ClaudeEnv: c.ClaudeEnv, ClaudeModel: c.ClaudeModel, ClaudeEffort: c.ClaudeEffort, ClaudeDefaultModel: c.claudeDefaultModel, ClaudeModels: snapshotClaudeModels(c.claudeModels),
		PiCommand: c.Pi.Command, PiError: c.Pi.Error, PiBinary: c.Pi.Binary, PiArgs: c.Pi.Args, PiEnv: c.Pi.Env, PiModel: c.Pi.Model, PiEffort: c.Pi.Effort, AgentEnabled: c.AgentSwitches(), AgentUpdates: c.AgentUpdates(),
		Account: c.AccountState, AccountReset: c.AccountReset, QuotaLimits: c.QuotaLimits, QuotaStatus: c.quotaStatus,
		Phone: DesktopPhone.Status(),
	}
}

var _ serviceserver.Host = (*Controller)(nil)
