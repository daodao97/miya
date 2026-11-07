package controller

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"miya/internal/agent/account"
	claude "miya/internal/agent/claude/native"
	codex "miya/internal/agent/codex/native"
	codexprojection "miya/internal/agent/codex/projection"
	integrations "miya/internal/agent/integrations"
	agentpolicy "miya/internal/agent/policy"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	"miya/internal/automation"
	threadlist "miya/internal/chat/history"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/desktop/control"
	"miya/internal/mcp/access"
	mcpconfig "miya/internal/mcp/config"
	"miya/internal/memory/manager"
	"miya/internal/notification"
	"miya/internal/prefs"
	projectmodel "miya/internal/project/model"
	projectregistry "miya/internal/project/registry"
	"miya/internal/runtime/tracker"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/textutil"
	"miya/internal/uiruntime"
)

type Controller struct {
	appmodel.WindowState

	desktopMu   sync.Mutex
	Desktop     *control.Server
	runtimeMu   sync.Mutex
	runtimePath string
	runtime     *tracker.Manager
	mem         manager.Manager // memory state; reach it through memories

	// auto is the automations, in the controller that runs them.
	Auto *automation.Runner
	// miyaPending are the answers Miya's own cards wait for.
	miyaPending map[string]chan bool
	miyaSerial  int
	// threadHistory holds referenced conversations' prefetched messages.
	threadHistory map[string][]model.Message

	referenceLoading map[model.ThreadRef]bool

	ReferenceDir     string
	ForkedReferences map[model.ThreadRef]referenceSession
	// handoffs track work explicitly delegated to another conversation.
	Handoffs []handoff
	// delegationParent is the conversation that handed each its work.
	DelegationParent      map[int]int
	Collaboration         *collaborationState
	CollaborationRemote   *model.CollaborationStatus
	mcpSessionAccess      access.Grants
	fastMode              bool
	Push                  *notification.Hub
	ClaudeBinary          string
	ClaudeError           string
	ClaudeArgs, ClaudeEnv []string
	ClaudeCommand         string
	// claudeModel and claudeEffort are Claude Code's choices, "" its default.
	ClaudeModel, ClaudeEffort string
	// claudeDefaultModel is the model Claude Code uses when none is chosen.
	claudeDefaultModel string

	ClaudeRuns map[int]*claude.Client
	// Pi is the pi agent's command, choices and running processes.
	Pi piState
	// agents records which agents the user turned on or off, and installs
	// caches whether their CLIs are installed.
	agents   agentSwitches
	installs map[string]agentInstall
	// claudeCmdCache keeps Claude Code\'s slash commands, by project.
	ClaudeCmdCache    map[string]ClaudeCommandList
	claudeCmdLoading  map[string]bool
	ClaudeHistoryDone chan struct{}
	// onDraftReturned gives a message that could not be sent back to the window.
	OnDraftReturned func(id int, text string, attachments []string)
	Mirror          *svcclient.Mirror
	// run runs dispatched work; nil uses runOnMain.
	Run          func(func())
	AccountCache account.Display

	// Account display lives in WindowState; quota* tracks composer refreshes.
	accountVersion     int
	accountCancel      context.CancelFunc
	QuotaLimits        account.Limits
	quotaStatus        string
	quotaVersion       int
	quotaCancel        context.CancelFunc
	DraftPermission    func() string
	SetDraftPermission func(string)

	SetProjects func([]projectmodel.Project)

	SetThreads func([]model.Thread)

	SetDraft func(string)

	SetModel func(string)

	SetEffort        func(string)
	PermissionPolicy func() string

	Locale    func() string
	SetLocale func(string)
	Models    func() []codex.Model
	SetModels func([]codex.Model)

	SetAttachments func([]string)
	Status         func() string

	SetReady func(bool)

	SetApprovals func([]codex.Approval)

	Client *codex.Client

	Serial          int
	connectionID    int
	Closed          bool
	RemoteTerminals atomic.Int64

	Connecting       bool
	connectionCancel context.CancelFunc

	DesktopReadPending map[string]int64 // window acknowledgements awaiting a service snapshot

	SetRevision func(int)
}

const (
	threadPageSize    = 20
	maxVisibleThreads = 200
)

func newChatController() *Controller {
	c := &Controller{WindowState: appmodel.WindowState{ProjectCache: make(map[string][]model.Thread)}, fastMode: prefs.Load("codexServiceTier") == "fast"}
	c.ReferenceSource, c.LastSubmitError = model.ReferenceSourceForService()
	c.ReferenceDir = appdir.Dir()
	c.LoadForkedReferences()
	var setProjects func([]projectmodel.Project)
	c.Projects, setProjects = uiruntime.CreateSignal([]projectmodel.Project{})
	c.SetProjects = func(projects []projectmodel.Project) {
		setProjects(projects)
		projectregistry.SaveProjectOrder(projects)
	}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.InstallNotifications()
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Agent, c.SetAgent = uiruntime.CreateSignal("codex")
	c.applyClaudeCommand(claude.LoadCommand())
	c.ClaudeModel, c.ClaudeEffort = prefs.Load("claudeModel"), prefs.Load("claudeEffort")
	c.claudeDefaultModel = prefs.Load("claudeDefaultModel")
	c.loadPiSettings()
	c.agents = loadAgentSwitches()
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Model, c.SetModel = uiruntime.CreateSignal("")
	c.Effort, c.SetEffort = uiruntime.CreateSignal("")
	c.DraftPermission, c.SetDraftPermission = uiruntime.CreateSignal("")
	initialPermissionPolicy := prefs.LoadPermissionPolicy()
	var setPermissionPolicy func(string)
	c.PermissionPolicy, setPermissionPolicy = uiruntime.CreateSignal(initialPermissionPolicy)
	c.SetPermissionPolicy = func(policy string) {
		policy = agentpolicy.Normalize(policy)
		setPermissionPolicy(policy)
		prefs.SavePermissionPolicy(policy)
	}
	initialLocale := prefs.LoadLocale()
	var setLocale func(string)
	c.Locale, setLocale = uiruntime.CreateSignal(initialLocale)
	c.SetLocale = func(locale string) {
		locale = prefs.NormalizeLocale(locale)
		setLocale(locale)
		prefs.SaveLocale(locale)
	}
	c.Models, c.SetModels = uiruntime.CreateSignal([]codex.Model{})
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.OnDraftReturned = c.RestoreDraft
	c.HistoryStatus, c.SetHistoryStatus = uiruntime.CreateSignal("Loading conversations…")
	c.Revision, c.SetRevision = uiruntime.CreateSignal(0)
	c.Status, c.SetStatus = uiruntime.CreateSignal(textutil.FirstNonEmpty(c.LastSubmitError, "Connecting to Codex…"))
	c.quotaStatus = "Loading quota…"
	c.Ready, c.SetReady = uiruntime.CreateSignal(false)
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.installApprovalNotifications()
	c.Cwd = os.Getenv("QUICKGUI_WORKSPACE")
	if c.Cwd == "" {
		c.Cwd, _ = os.Getwd()
	}
	c.Cwd, _ = filepath.Abs(c.Cwd)
	c.ProjectCache = threadlist.Load()
	c.LoadDesktopUnread()
	c.AssignCachedThreadIDs()
	projects := projectregistry.LoadProjects(c.Cwd, integrations.Projects())
	cachedPaths := make([]string, 0, len(c.ProjectCache))
	for path := range c.ProjectCache {
		cachedPaths = append(cachedPaths, path)
	}
	sort.Strings(cachedPaths)
	for _, path := range cachedPaths {
		found := false
		for _, project := range projects {
			if project.Path == path {
				found = true
				break
			}
		}
		if !found {
			projects = append(projects, projectmodel.Project{Name: filepath.Base(path), Path: path})
		}
	}
	c.SetProjects(projectregistry.RestoreProjectOrder(projects))
	if cached := c.ProjectCache[c.Cwd]; len(cached) > 0 {
		c.SetThreads(cached)
		c.SetHistoryStatus("")
	}
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	uiruntime.OnCleanup(func() {
		for _, t := range c.Threads() {
			c.BackendOf(t).Suspend(t)
		}
		c.Closed = true
		c.Cancel()
		if c.Client != nil {
			c.Client.Close()
		}
	})
	c.discoverAll()
	return c
}

func (c *Controller) SelectedPermissionPolicy() string {
	if c.PermissionPolicy == nil {
		return "on-request"
	}
	return agentpolicy.Normalize(c.PermissionPolicy())
}

func (c *Controller) rememberProject(path string) {
	if items, added := projectregistry.Remember(c.Projects(), path); added {
		c.SetProjects(items)
		integrations.RememberProject(path)
	}
}

func (c *Controller) RenameProject(path, name string) {
	if c.Remote != nil {
		c.forward("renameProject", svcproto.ProjectRequest{Path: path, Name: name})
		return
	}
	if items, changed := projectregistry.Rename(c.Projects(), path, name); changed {
		c.SetProjects(items)
	}
}

// removeThreadLocal removes a thread from the visible list and its persisted
// project cache. The Codex archive call is intentionally separate so the UI
// can also remove locally-created threads that have no server ID yet.
func (c *Controller) removeThreadLocal(id int, serverID string) {
	project := c.ThreadWorkspace(id)
	withoutThread := func(items []model.Thread) []model.Thread {
		filtered := make([]model.Thread, 0, len(items))
		for _, item := range items {
			if item.ID == id || (serverID != "" && item.ServerID == serverID) {
				continue
			}
			filtered = append(filtered, item)
		}
		return filtered
	}
	if project == c.Cwd {
		filtered := withoutThread(c.Threads())
		c.SetThreads(filtered)
		if c.Active() == id {
			if len(filtered) > 0 {
				c.SelectThread(filtered[0])
			} else {
				c.NewThread()
			}
		}
	}
	if c.ProjectCache != nil && project != "" {
		c.ProjectCache[project] = withoutThread(c.ProjectCache[project])
	}
	if project != c.Cwd && c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
	threadlist.Persist(c.ProjectCache)
	c.RefreshProjectOrder()
}

func (c *Controller) ArchiveThread(t model.Thread) {
	if c.Remote != nil {
		c.forward("archive", t.ID)
		return
	}
	c.BackendOf(t).Archive(t)
}

func (c *Controller) archiveCodexThread(t model.Thread) { c.codexAdapter().Archive(t) }

func (c *Controller) DeleteThread(t model.Thread) {
	if c.Remote != nil {
		c.forward("delete", t.ID)
		return
	}
	c.BackendOf(t).Delete(t)
}

func (c *Controller) deleteCodexThread(t model.Thread) { c.codexAdapter().Delete(t) }

func (c *Controller) removeProject(path string, deleteConfig bool) {
	path, _ = filepath.Abs(path)
	c.SetProjects(projectregistry.Remove(c.Projects(), path))
	delete(c.ProjectCache, path)
	threadlist.Persist(c.ProjectCache)
	if deleteConfig {
		integrations.ForgetProject(path)
	}
}

func (c *Controller) ArchiveProject(path string) {
	if c.Remote != nil {
		c.forward("archiveProject", svcproto.ProjectRequest{Path: path})
		return
	}
	for _, thread := range append([]model.Thread(nil), c.ProjectCache[path]...) {
		c.ArchiveThread(thread)
	}
	c.removeProject(path, false)
}

func (c *Controller) DeleteProject(path string) {
	if c.Remote != nil {
		c.forward("deleteProject", svcproto.ProjectRequest{Path: path})
		return
	}
	for _, thread := range append([]model.Thread(nil), c.ProjectCache[path]...) {
		c.DeleteThread(thread)
	}
	c.removeProject(path, true)
}

func (c *Controller) Dispatch(f func()) {
	run := c.Run
	if run == nil {
		run = uiruntime.RunOnMain
	}
	run(func() {
		if !c.Closed {
			f()
		}
	})
}

func (c *Controller) NextID() int { c.Serial++; return c.Serial }

// Keep published IDs across launches: older phones subscribe by this number.
// Repair invalid/duplicate cache entries above the highest restored ID.
func (c *Controller) AssignCachedThreadIDs() {
	paths := make([]string, 0, len(c.ProjectCache))
	for path, threads := range c.ProjectCache {
		paths = append(paths, path)
		for _, t := range threads {
			c.Serial = max(c.Serial, t.ID)
		}
	}
	sort.Strings(paths)
	seen := map[int]bool{}
	for _, path := range paths {
		threads := c.ProjectCache[path]
		for i := range threads {
			if threads[i].ID <= 0 || seen[threads[i].ID] {
				threads[i].ID = c.NextID()
			}
			seen[threads[i].ID] = true
		}
		c.ProjectCache[path] = threads
	}
}

func (c *Controller) Current() model.Thread {
	for _, t := range c.Threads() {
		if t.ID == c.Active() {
			return t
		}
	}
	return model.Thread{Title: tr("New thread"), Project: filepath.Base(c.Cwd)}
}

func (c *Controller) ProjectBusy(path string) bool {
	threads := c.ProjectCache[path]
	if path == c.Cwd {
		threads = c.Threads()
	} else if c.Mirror != nil {
		return c.Mirror.ProjectBusy[path]
	}
	for _, t := range threads {
		if t.Busy && !t.Loading {
			return true
		}
	}
	return false
}

func (c *Controller) Update(id int, fn func(*model.Thread)) {
	items := append([]model.Thread(nil), c.Threads()...)
	for i := range items {
		if items[i].ID == id {
			items[i].Messages = append([]model.Message(nil), items[i].Messages...)
			items[i].Tasks = append([]model.ThreadTask(nil), items[i].Tasks...)
			old := items[i]
			before := threadlist.MetaOf(items[i])
			wasBusy := items[i].Busy
			fn(&items[i])
			if items[i].ExecutionID != "" && len(items[i].Messages) >= len(old.Messages) {
				for j := len(old.Messages); j < len(items[i].Messages); j++ {
					if items[i].Messages[j].ExecutionID == "" {
						items[i].Messages[j].ExecutionID = items[i].ExecutionID
					}
				}
			}
			notification.UpdateCompletionBody(old, &items[i])
			if old.ServerID != items[i].ServerID && old.ServerID != "" {
				c.rememberForkedReference(old, c.Cwd)
			}
			c.SetThreads(items)
			if c.ProjectCache != nil && c.Cwd != "" {
				c.ProjectCache[c.Cwd] = append([]model.Thread(nil), items...)
			}
			// Streamed text changes no metadata: reorder and write the sidebar's
			// cache only when what it keeps changed, not for every delta.
			if threadlist.MetaOf(items[i]) != before {
				c.RefreshProjectOrder()
				threadlist.Persist(c.ProjectCache)
			}
			if c.Revision != nil {
				c.SetRevision(c.Revision() + 1)
			}
			if wasBusy && !items[i].Busy && len(items[i].Queue) > 0 {
				done := items[i].ID
				c.Dispatch(func() { c.drainQueue(done) })
			}
			if len(c.Handoffs) > 0 {
				c.refreshHandoffs(id)
			}
			return
		}
	}
	// Background projects retain live sessions and must receive stream and
	// asynchronous request results even while another project is selected.
	for path, cached := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		for i := range cached {
			if cached[i].ID != id {
				continue
			}
			items := append([]model.Thread(nil), cached...)
			old := items[i]
			items[i].Messages = append([]model.Message(nil), old.Messages...)
			items[i].Tasks = append([]model.ThreadTask(nil), old.Tasks...)
			fn(&items[i])
			if items[i].ExecutionID != "" && len(items[i].Messages) >= len(old.Messages) {
				for j := len(old.Messages); j < len(items[i].Messages); j++ {
					if items[i].Messages[j].ExecutionID == "" {
						items[i].Messages[j].ExecutionID = items[i].ExecutionID
					}
				}
			}
			notification.UpdateCompletionBody(old, &items[i])
			if old.ServerID != items[i].ServerID && old.ServerID != "" {
				c.rememberForkedReference(old, path)
			}
			c.ProjectCache[path] = items
			c.RecordDesktopCompletion(old, items[i])
			if threadlist.MetaOf(items[i]) != threadlist.MetaOf(old) {
				c.RefreshProjectOrder()
				threadlist.Persist(c.ProjectCache)
			}
			if c.Revision != nil {
				c.SetRevision(c.Revision() + 1)
			}
			if n, ok := notification.Completion(old, items[i]); ok {
				c.deliverNotice(n, "completion")
			}
			if old.Busy && !items[i].Busy && len(items[i].Queue) > 0 {
				c.Dispatch(func() { c.drainQueue(id) })
			}
			return
		}
	}
}

func (c *Controller) RefreshProjectOrder() {
	if c.Projects == nil || c.SetProjects == nil {
		return
	}
	projects := append([]projectmodel.Project(nil), c.Projects()...)
	latest := func(path string) int64 {
		var result int64
		for _, t := range c.ProjectCache[path] {
			if t.LastActivityAt > result {
				result = t.LastActivityAt
			}
		}
		return result
	}
	sort.SliceStable(projects, func(i, j int) bool {
		li, lj := latest(projects[i].Path), latest(projects[j].Path)
		if li != lj {
			return li > lj
		}
		return false
	})
	for i, project := range projects {
		if project != c.Projects()[i] {
			c.SetProjects(projects)
			return
		}
	}
}

func (c *Controller) Connect() {
	if c.Remote != nil {
		c.forward("connect", nil)
		return
	}
	if c.Connecting || c.Ready() {
		return
	}
	c.resetCodexSessions()
	c.Connecting = true
	c.connectionID++
	generation, cwd := c.connectionID, c.Cwd
	if c.connectionCancel != nil {
		c.connectionCancel()
	}
	connectionCtx, cancel := context.WithCancel(c.Ctx)
	c.connectionCancel = cancel
	c.SetStatus("Connecting to Codex…")
	c.SetHistoryStatus("Loading conversations…")
	go func() {
		ctx, done := context.WithTimeout(connectionCtx, 15*time.Second)
		client, err := codex.Start(ctx, "", cwd, mcpconfig.CodexArgs(cwd)...)
		done()
		if err != nil {
			c.Dispatch(func() {
				if c.connectionID != generation {
					return
				}
				c.Connecting = false
				c.SetStatus(err.Error())
				c.SetHistoryStatus("Unable to load conversations: " + err.Error() + ". Reconnect to retry.")
			})
			return
		}
		go func() {
			select {
			case <-connectionCtx.Done():
				client.Close()
			case <-client.Done():
			}
		}()
		c.Dispatch(func() {
			if c.connectionID != generation {
				go client.Close()
				return
			}
			c.Client = client
			c.Connecting = false
			c.SetReady(true)
			c.SetStatus("Connected · Codex app-server")
			c.QuotaLimits = account.Limits{}
			c.refreshQuota()
			c.loadThreadList(connectionCtx, client, c.Cwd, generation)
			c.loadModels(connectionCtx, client, generation)
		})
		for frame := range client.Events() {
			f := frame
			c.Dispatch(func() {
				if c.Client == client && c.connectionID == generation {
					c.Handle(f)
				}
			})
		}
		c.Dispatch(func() {
			if c.Client != client || c.connectionID != generation {
				return
			}
			c.SetReady(false)
			c.SetApprovals(nil)
			c.SetStatus(client.Err().Error())
			c.SetHistoryStatus("Connection lost. Reconnect to retry.")
			c.resetCodexSessions()
		})
	}()
}

func (c *Controller) loadThreadList(ctx context.Context, client *codex.Client, cwd string, generation int) {
	projectPaths := []string{cwd}
	for _, project := range c.Projects() {
		if project.Path != "" && project.Path != cwd {
			projectPaths = append(projectPaths, project.Path)
		}
	}
	watchdog := time.AfterFunc(20*time.Second, func() {
		c.Dispatch(func() {
			if c.Client == client && c.connectionID == generation && c.HistoryStatus() == "Loading conversations…" {
				c.SetHistoryStatus("History request timed out. Reconnect to retry.")
			}
		})
	})

	type result struct {
		cwd  string
		data []codex.Thread
		err  error
	}
	var received int
	var firstErr error
	apply := func(result result) {
		c.Dispatch(func() {
			if c.Client != client || c.connectionID != generation {
				return
			}
			received++
			if result.err != nil && firstErr == nil {
				firstErr = result.err
			}
			if c.ProjectCache == nil {
				c.ProjectCache = make(map[string][]model.Thread)
			}

			// Merge this project as soon as its own request completes. The active
			// project is published immediately; slower projects append later.
			projectPath, _ := filepath.Abs(result.cwd)
			items := c.ProjectCache[projectPath]
			if projectPath == c.Cwd {
				items = append([]model.Thread(nil), c.Threads()...)
				for _, local := range c.ProjectCache[projectPath] {
					found := false
					for _, item := range items {
						if item.ID == local.ID || (item.ServerID != "" && item.ServerID == local.ServerID) {
							found = true
							break
						}
					}
					if !found {
						items = append(items, local)
					}
				}
			}
			for _, remote := range result.data {
				found := false
				for i := range items {
					if model.ThreadAgent(items[i]) == appmodel.AgentCodex && items[i].ServerID == remote.ID {
						found = true
						items[i].Title = msgfmt.ConversationTitle(remote.Name, remote.Preview, items[i].Title)
						items[i].Age = time.Unix(remote.UpdatedAt, 0).Format("Jan 2")
						items[i].LastActivityAt = max(items[i].LastActivityAt, remote.UpdatedAt*1000)
						break
					}
				}
				if !found {
					items = append(items, model.Thread{ID: c.NextID(), ServerID: remote.ID, Title: msgfmt.ConversationTitle(remote.Name, remote.Preview, ""), Project: filepath.Base(projectPath), Age: time.Unix(remote.UpdatedAt, 0).Format("Jan 2"), LastActivityAt: remote.UpdatedAt * 1000})
				}
			}
			if len(items) > maxVisibleThreads {
				items = items[:maxVisibleThreads]
			}
			c.ProjectCache[projectPath] = append([]model.Thread(nil), items...)
			if projectPath == c.Cwd {
				c.SetThreads(items)
			}
			if received == len(projectPaths) {
				c.RefreshProjectOrder()
			}
			threadlist.Persist(c.ProjectCache)
			// Any successful project means the blocking loading state is over.
			if result.err == nil || len(result.data) > 0 {
				c.SetHistoryStatus("")
			} else if received == len(projectPaths) && len(c.Threads()) == 0 && firstErr != nil {
				c.SetHistoryStatus("History unavailable: " + firstErr.Error())
			}
		})
	}

	sem := make(chan struct{}, 4)
	for _, projectPath := range projectPaths {
		projectPath := projectPath
		go func() {
			if info, err := os.Stat(projectPath); err != nil || !info.IsDir() {
				apply(result{cwd: projectPath, err: err})
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			var history struct {
				Data []codex.Thread `json:"data"`
			}
			err := client.Call(callCtx, "thread/list", map[string]any{
				"cwd": projectPath, "limit": threadPageSize, "sortKey": "updated_at",
				"modelProviders": []string{}, "sourceKinds": []string{"cli", "vscode", "exec", "appServer", "unknown"},
			}, &history)
			if len(history.Data) > threadPageSize {
				history.Data = history.Data[:threadPageSize]
			}
			apply(result{cwd: projectPath, data: history.Data, err: err})
		}()
	}
	go func() {
		<-ctx.Done()
		watchdog.Stop()
	}()
}

func (c *Controller) loadModels(ctx context.Context, client *codex.Client, generation int) {
	go func() {
		var models struct {
			Data []codex.Model `json:"data"`
		}
		var config struct {
			Config struct {
				Model string `json:"model"`
			} `json:"config"`
		}
		if err := client.Call(ctx, "model/list", map[string]any{"limit": 100}, &models); err != nil {
			return
		}
		_ = client.Call(ctx, "config/read", map[string]any{}, &config)
		c.Dispatch(func() {
			if c.Client != client || c.connectionID != generation {
				return
			}
			c.SetModels(models.Data)
			selected := config.Config.Model
			if selected == "" {
				for _, m := range models.Data {
					if m.IsDefault {
						selected = m.Model
						break
					}
				}
			}
			if selected == "" && len(models.Data) > 0 {
				selected = models.Data[0].Model
			}
			c.SelectModel(selected)
		})
	}()
}

func (c *Controller) readHistory(t model.Thread) {
	c.Resume(t, nil)
}

// changeWorkspace changes the selected project while keeping Codex sessions
// alive on the shared app-server. The native directory picker validates that
// the user selected a directory, but keep this check here as well because the
// controller is also used by tests and other callers.
func (c *Controller) ChangeWorkspace(path string) error {
	if c.Remote != nil {
		return c.remoteWorkspace(path)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("workspace path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace is not a directory: %s", abs)
	}
	if abs == c.Cwd {
		return nil
	}

	if c.ProjectCache == nil {
		c.ProjectCache = make(map[string][]model.Thread)
	}
	cached := append([]model.Thread(nil), c.Threads()...)
	for i := range cached {
		// A running conversation goes on in the background, as Codex's do;
		// its frames follow it to the project it stays in.
		if cached[i].Busy || c.ClaudeRuns[cached[i].ID] != nil {
			continue
		}
		if model.ThreadAgent(cached[i]) != appmodel.AgentCodex {
			c.BackendOf(cached[i]).Suspend(cached[i])
			cached[i].Loaded = false
			cached[i].Loading = false
			cached[i].Busy = false
			cached[i].TurnID = ""
		}
	}
	c.ProjectCache[c.Cwd] = cached
	c.Cwd = abs
	c.rememberProject(abs)
	cachedThreads := append([]model.Thread(nil), c.ProjectCache[abs]...)
	c.SetThreads(cachedThreads)
	if len(cachedThreads) > 0 {
		c.SetHistoryStatus("")
	} else {
		c.SetHistoryStatus("Loading conversations…")
	}
	c.SetActive(0)
	c.SetDraft("")
	if c.Ready() && c.Client != nil {
		c.loadThreadList(c.Ctx, c.Client, abs, c.connectionID)
	}
	c.discoverAll()
	return nil
}

func (c *Controller) SelectModel(value string) {
	if c.Remote != nil {
		c.SetModel(value)
		c.forward("model", value)
		return
	}
	c.SetModel(value)
	c.SetEffort("")
	for _, m := range c.Models() {
		if m.Model == value {
			c.SetEffort(SavedCodexEffort(m))
			return
		}
	}
}

func (c *Controller) NewThread() {
	if c.SetDraftPermission != nil {
		c.SetDraftPermission("")
	}
	c.SetActive(0)
	c.SetDraft("")
	c.SetAttachments(nil)
}

func (c *Controller) RememberDraft(value string) {
	c.SetDraft(value)
	if c.Remote != nil {
		if c.Active() != 0 {
			if c.Mirror != nil {
				if c.Mirror.Drafts == nil {
					c.Mirror.Drafts = map[int]string{}
				}
				c.Mirror.Drafts[c.Active()] = value
			}
			c.forward("draft", svcproto.ThreadValue{Thread: c.Active(), Value: value})
		}
		return
	}
	c.Update(c.Active(), func(t *model.Thread) { t.Draft = value })
}

func (c *Controller) AddAttachments(paths []string) {
	items := append([]string(nil), c.Attachments()...)
	seen := make(map[string]bool, len(items))
	for _, path := range items {
		seen[path] = true
	}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" && !seen[path] {
			items = append(items, path)
			seen[path] = true
		}
	}
	c.SetAttachments(items)
}

func (c *Controller) RemoveAttachment(path string) {
	items := make([]string, 0, len(c.Attachments()))
	for _, item := range c.Attachments() {
		if item != path {
			items = append(items, item)
		}
	}
	c.SetAttachments(items)
}

func (c *Controller) AttachmentInputs(paths []string) []map[string]any {
	inputs := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		if strings.HasPrefix(mime.TypeByExtension(filepath.Ext(path)), "image/") {
			inputs = append(inputs, map[string]any{"type": "localImage", "path": path})
			continue
		}
		inputs = append(inputs, map[string]any{"type": "text", "text": "Attached file: " + path, "text_elements": []any{}})
	}
	return inputs
}

func (c *Controller) SelectThread(t model.Thread) {
	// Viewing history does not count as conversation activity.
	if latest := c.ThreadWithID(t.ID); latest.ID != 0 {
		t = latest
	}
	c.SetActive(t.ID)
	draft := t.Draft
	if c.Mirror != nil {
		if local, ok := c.Mirror.Drafts[t.ID]; ok {
			draft = local
		}
	}
	c.SetDraft(draft)
	c.SetAttachments(nil)
	if c.Remote != nil {
		c.forward("open", t.ID)
		return
	}
	c.BackendOf(t).Open(t)
}

// resume continues a Codex thread, then submits after, when given.
func (c *Controller) Resume(t model.Thread, after *model.SendRequest) {
	if c.Remote != nil {
		c.forward("resume", t.ID)
		return
	}
	c.codexAdapter().Resume(t, after)
}

func (c *Controller) HistoryMessages(remote codex.Thread) []model.Message {
	out := codexprojection.HistoryMessages(remote)
	for i := range out {
		out[i].ID = c.NextID()
	}
	return out
}

// send submits the window's draft in its open conversation.
func (c *Controller) Send() { c.SendDraft(false) }

// sendDraft submits the window's draft; in a running turn it steers the
// turn, or waits in the queue when steer is false.
func (c *Controller) SendDraft(steer bool) int {
	req := model.SendRequest{Steer: steer, ThreadID: c.Current().ID, Text: c.Draft(), Attachments: append([]string(nil), c.Attachments()...), Agent: c.CurrentAgent(), Model: c.CurrentBackend().Model(), Effort: c.CurrentBackend().Effort(), Policy: c.CurrentPermissionPolicy()}
	id := c.Submit(req)
	if id != 0 {
		c.SetActive(id)
		c.RememberDraft("")
		c.SetAttachments(nil)
	}
	return id
}

func (c *Controller) Stop() { c.StopThread(c.Current().ID) }

func (c *Controller) StopThread(id int) {
	if c.Remote != nil {
		c.forward("stop", id)
		return
	}
	t := c.ThreadWithID(id)
	if c.Collaboration != nil {
		ref := c.ThreadRef(t).URL()
		if ref != "" {
			c.Collaboration.Pause(ref)
		}
	}
	if !t.Busy && c.ClaudeRuns[t.ID] == nil {
		return
	}
	c.Update(t.ID, func(t *model.Thread) { t.StopRequested = true; t.Status = "Stopping…" })
	c.BackendOf(t).Stop(t)
}

func (c *Controller) interrupt(t model.Thread) { c.codexAdapter().Interrupt(t) }

func (c *Controller) permissionForThread(t model.Thread) string {
	if t.PermissionPolicy != "" {
		return agentpolicy.Normalize(t.PermissionPolicy)
	}
	return c.SelectedPermissionPolicy()
}

func (c *Controller) CurrentPermissionPolicy() string {
	t := c.Current()
	if t.ID == 0 && c.DraftPermission != nil && c.DraftPermission() != "" {
		return agentpolicy.Normalize(c.DraftPermission())
	}
	return c.permissionForThread(t)
}

func (c *Controller) SetCurrentPermissionPolicy(policy string) {
	policy = agentpolicy.Normalize(policy)
	if c.Current().ID == 0 {
		if c.SetDraftPermission != nil {
			c.SetDraftPermission(policy)
		}
		return
	}
	if c.Remote != nil {
		c.forward("threadPolicy", svcproto.ThreadValue{Thread: c.Active(), Value: policy})
		return
	}
	c.Update(c.Active(), func(t *model.Thread) { t.PermissionPolicy = policy })
}

// onMain runs f on the main thread and waits for it.
func (c *Controller) onMain(f func() (any, error)) (any, error) {
	type result struct {
		v   any
		err error
	}
	done := make(chan result, 1)
	c.Dispatch(func() {
		v, err := f()
		done <- result{v, err}
	})
	select {
	case r := <-done:
		return r.v, r.err
	case <-time.After(30 * time.Second):
		return nil, errors.New("Miya is not responding")
	}
}
