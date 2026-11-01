package main

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"miya/internal/claude"
	"miya/internal/codex"
)

var appLocale atomic.Value

var translations = map[string]string{
	"New thread": "新建对话", "Automations": "自动化", "Skills": "技能", "Projects": "项目", "Settings": "设置", "Open": "打开", "Commit": "提交",
	"Reconnecting…": "正在重连…",
	"Thinking…":     "思考中…", "Thought": "已思考", "Ran ": "已运行 ", "Running ": "正在运行 ", "Failed · ": "失败 · ", "Stopped · ": "已停止 · ", "Edited": "已编辑", "Editing": "正在编辑", "Failed to edit": "编辑失败", "Proposed plan": "建议计划", "Changes in this turn": "本回合更改", "No matching commands": "没有匹配的命令", "Searching project files…": "正在搜索项目文件…", "No matching files": "没有匹配的文件", "Project files": "项目文件", "Commands": "命令", "Ask Codex anything… / commands · @ files": "询问 Codex… / 命令 · @ 文件", "Message Codex": "发送消息给 Codex", "Copy code": "复制代码", "Copied": "已复制", "Copy failed": "复制失败", "Reconnect": "重新连接", "Close": "关闭", "Local": "本地", "Not connected yet": "尚未连接",
	"Worked for ": "已工作 ", "Working for ": "工作中 ", "seconds": "秒",
	"Connecting to Codex…": "正在连接 Codex…", "Connected · Codex app-server": "已连接 Codex", "Connection lost. Reconnect to retry.": "连接已断开，请重新连接。", "History unavailable": "历史记录不可用", "Working…": "工作中…", "Starting…": "正在启动…", "Codex is reconnecting…": "Codex 正在重新连接…", "Loading conversation…": "正在加载对话…", "Stopped": "已停止", "Turn failed": "回合失败",
}

func tr(english string) string {
	if value, ok := appLocale.Load().(string); ok && value == "zh" {
		if translated, exists := translations[english]; exists {
			return translated
		}
	}
	return english
}

type chatController struct {
	// auto is the automations, in the controller that runs them.
	auto *automationState
	// miyaPending are the answers Miya's own cards wait for.
	miyaPending map[string]chan bool
	miyaSerial  int
	// threadHistory holds referenced conversations' prefetched messages.
	threadHistory map[int][]Message
	// handoffs link referenced conversations to where they were sent.
	handoffs []handoff
	// delegationParent is the conversation that handed each its work.
	delegationParent      map[int]int
	mcpSessionAccess      map[string]bool
	fastMode              bool
	push                  *pushHub
	claudeBinary          string
	claudeError           string
	claudeArgs, claudeEnv []string
	claudeCommand         string
	// claudeModel and claudeEffort are Claude Code's choices, "" its default.
	claudeModel, claudeEffort string
	// claudeDefaultModel is the model Claude Code uses when none is chosen.
	claudeDefaultModel string
	agent              func() string
	setAgent           func(string)
	claudeRuns         map[int]*claude.Client
	// claudeCmdCache keeps Claude Code\'s slash commands, by project.
	claudeCmdCache   map[string]claudeCommandList
	claudeCmdLoading map[string]bool
	// commandsChanged hears a project\'s Claude Code commands arrive.
	commandsChanged   func()
	claudeHistoryDone chan struct{}
	// onDraftReturned gives a message that could not be sent back to the window.
	onDraftReturned func(id int, text string, attachments []string)
	// remote is the service a window's controller mirrors, nil when the
	// controller runs the agents itself.
	remote *serviceClient
	mirror *mirrorState
	// run runs dispatched work; nil uses runOnMain.
	run          func(func())
	accountCache accountDisplay
	accountReset accountResetAction
	// accountState is what the account page shows; quota* the composer's ring.
	accountState        accountDisplay
	accountVersion      int
	accountCancel       context.CancelFunc
	quotaLimits         accountLimits
	quotaStatus         string
	quotaVersion        int
	quotaCancel         context.CancelFunc
	draftPermission     func() string
	setDraftPermission  func(string)
	projects            func() []Project
	setProjects         func([]Project)
	threads             func() []Thread
	setThreads          func([]Thread)
	active              func() int
	setActive           func(int)
	draft               func() string
	setDraft            func(string)
	model               func() string
	setModel            func(string)
	effort              func() string
	setEffort           func(string)
	permissionPolicy    func() string
	setPermissionPolicy func(string)
	locale              func() string
	setLocale           func(string)
	models              func() []codex.Model
	setModels           func([]codex.Model)
	attachments         func() []string
	setAttachments      func([]string)
	status              func() string
	setStatus           func(string)
	ready               func() bool
	setReady            func(bool)
	approvals           func() []codex.Approval
	setApprovals        func([]codex.Approval)
	cwd                 string
	client              *codex.Client
	ctx                 context.Context
	cancel              context.CancelFunc
	serial              int
	connectionID        int
	closed              bool
	connecting          bool
	connectionCancel    context.CancelFunc
	projectCache        map[string][]Thread
	historyStatus       func() string
	setHistoryStatus    func(string)
	revision            func() int
	setRevision         func(int)
}

type Project struct {
	Name string
	Path string
}

type threadListCache struct {
	Projects map[string][]Thread `json:"projects"`
}

const (
	threadPageSize    = 20
	maxVisibleThreads = 200
)

func newChatController() *chatController {
	c := &chatController{projectCache: make(map[string][]Thread), fastMode: loadSetting("codexServiceTier") == "fast"}
	var setProjects func([]Project)
	c.projects, setProjects = createSignal([]Project{})
	c.setProjects = func(projects []Project) { setProjects(projects); persistProjectOrder(projects) }
	c.threads, c.setThreads = createSignal([]Thread{})
	c.installNotifications()
	c.active, c.setActive = createSignal(0)
	c.agent, c.setAgent = createSignal("codex")
	c.applyClaudeCommand(loadClaudeCommand())
	c.claudeModel, c.claudeEffort = loadSetting("claudeModel"), loadSetting("claudeEffort")
	c.claudeDefaultModel = loadSetting("claudeDefaultModel")
	c.draft, c.setDraft = createSignal("")
	c.model, c.setModel = createSignal("")
	c.effort, c.setEffort = createSignal("")
	c.draftPermission, c.setDraftPermission = createSignal("")
	initialPermissionPolicy := loadPermissionPolicy()
	var setPermissionPolicy func(string)
	c.permissionPolicy, setPermissionPolicy = createSignal(initialPermissionPolicy)
	c.setPermissionPolicy = func(policy string) {
		policy = normalizePermissionPolicy(policy)
		setPermissionPolicy(policy)
		persistPermissionPolicy(policy)
	}
	initialLocale := loadLocale()
	appLocale.Store(initialLocale)
	var setLocale func(string)
	c.locale, setLocale = createSignal(initialLocale)
	c.setLocale = func(locale string) {
		locale = normalizeLocale(locale)
		appLocale.Store(locale)
		setLocale(locale)
		persistLocale(locale)
	}
	c.models, c.setModels = createSignal([]codex.Model{})
	c.attachments, c.setAttachments = createSignal([]string{})
	c.onDraftReturned = c.restoreDraft
	c.historyStatus, c.setHistoryStatus = createSignal("Loading conversations…")
	c.revision, c.setRevision = createSignal(0)
	c.status, c.setStatus = createSignal("Connecting to Codex…")
	c.quotaStatus = "正在读取额度…"
	c.ready, c.setReady = createSignal(false)
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	c.installApprovalNotifications()
	c.cwd = os.Getenv("QUICKGUI_WORKSPACE")
	if c.cwd == "" {
		c.cwd, _ = os.Getwd()
	}
	c.cwd, _ = filepath.Abs(c.cwd)
	c.projectCache = loadThreadListCache()
	c.assignCachedThreadIDs()
	projects := loadProjects(c.cwd)
	cachedPaths := make([]string, 0, len(c.projectCache))
	for path := range c.projectCache {
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
			projects = append(projects, Project{Name: filepath.Base(path), Path: path})
		}
	}
	c.setProjects(restoreProjectOrder(projects))
	if cached := c.projectCache[c.cwd]; len(cached) > 0 {
		c.setThreads(cached)
		c.setHistoryStatus("")
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	onCleanup(func() {
		for _, t := range c.threads() {
			backendOf(t).Suspend(c, t)
		}
		c.closed = true
		c.cancel()
		if c.client != nil {
			c.client.Close()
		}
	})
	c.discoverAll()
	return c
}

func normalizePermissionPolicy(policy string) string {
	switch policy {
	case "on-request", "on-failure", "never":
		return policy
	default:
		return "on-request"
	}
}

// Locale controls the language used by the native UI. Keep the persisted
// values deliberately small so older settings files remain forward compatible.
func normalizeLocale(locale string) string {
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "zh", "zh-cn", "zh_cn", "中文":
		return "zh"
	case "en", "en-us", "en_us", "english":
		return "en"
	default:
		return "en"
	}
}

func permissionPolicyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "quickgui-settings.json")
}

func threadListCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "quickgui-thread-cache.json")
}

func loadThreadListCache() map[string][]Thread {
	path := threadListCachePath()
	if path == "" {
		return map[string][]Thread{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]Thread{}
	}
	var cache threadListCache
	if json.Unmarshal(data, &cache) != nil || cache.Projects == nil {
		return map[string][]Thread{}
	}
	// Older caches persisted Resumed, but a Codex session belongs to the
	// app-server process that loaded it, not to the stored conversation.
	for _, threads := range cache.Projects {
		for i := range threads {
			resetCodexSession(&threads[i])
		}
	}
	return cache.Projects
}

// persistThreadListCache writes the sidebar's cache; tests replace it.
var persistThreadListCache = writeThreadListCache

// threadMeta is what the sidebar's cache keeps of a conversation.
type threadMeta struct {
	ID                                   int
	ServerID, Title, Agent, Project, Age string
	LastActivityAt                       int64
	Resumed                              bool
}

func threadMetaOf(t Thread) threadMeta {
	return threadMeta{t.ID, t.ServerID, t.Title, t.Agent, t.Project, t.Age, t.LastActivityAt, t.Resumed}
}

func writeThreadListCache(projects map[string][]Thread) {
	if projects == nil {
		return
	}
	path := threadListCachePath()
	if path == "" {
		return
	}
	// The sidebar only needs metadata. Never persist message bodies or tool output.
	metadata := make(map[string][]Thread, len(projects))
	for project, threads := range projects {
		for _, thread := range threads {
			metadata[project] = append(metadata[project], Thread{
				ID: thread.ID, ServerID: thread.ServerID, Title: thread.Title, Agent: thread.Agent,
				Resumed: threadAgent(thread) != agentCodex && thread.Resumed,
				Project: thread.Project, Age: thread.Age, LastActivityAt: thread.LastActivityAt,
			})
		}
	}
	data, err := json.Marshal(threadListCache{Projects: metadata})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

func loadPermissionPolicy() string {
	path := permissionPolicyPath()
	if path == "" {
		return "on-request"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "on-request"
	}
	var settings struct {
		PermissionPolicy string `json:"permissionPolicy"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return "on-request"
	}
	return normalizePermissionPolicy(settings.PermissionPolicy)
}

func loadLocale() string {
	path := permissionPolicyPath()
	if path == "" {
		return "en"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "en"
	}
	var settings struct {
		Locale string `json:"locale"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return "en"
	}
	return normalizeLocale(settings.Locale)
}

func persistLocale(locale string) {
	path := permissionPolicyPath()
	if path == "" {
		return
	}
	settings := map[string]string{"locale": normalizeLocale(locale)}
	// Preserve the permission preference when adding the locale to an existing
	// settings file. Unknown future keys are intentionally left untouched.
	if data, err := os.ReadFile(path); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil {
			existing["locale"] = settings["locale"]
			data, _ = json.Marshal(existing)
			_ = os.WriteFile(path, data, 0600)
			return
		}
	}
	data, _ := json.Marshal(settings)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

func persistPermissionPolicy(policy string) {
	path := permissionPolicyPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	settings["permissionPolicy"] = normalizePermissionPolicy(policy)
	data, err := json.Marshal(settings)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

func (c *chatController) selectedPermissionPolicy() string {
	if c.permissionPolicy == nil {
		return "on-request"
	}
	return normalizePermissionPolicy(c.permissionPolicy())
}

func sandboxForPermissionPolicy(policy string) string {
	if policy == "never" {
		return "danger-full-access"
	}
	return "workspace-write"
}

func loadProjects(current string) []Project {
	seen := map[string]bool{}
	projects := []Project{}
	add := func(path string) {
		path, _ = filepath.Abs(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		projects = append(projects, Project{Name: filepath.Base(path), Path: path})
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
			re := regexp.MustCompile(`(?m)^\[projects\."([^"]+)"\]`)
			for _, match := range re.FindAllStringSubmatch(string(data), -1) {
				if len(match) > 1 {
					add(match[1])
				}
			}
		}
	}
	add(current)
	return projects
}

func (c *chatController) rememberProject(path string) {
	items := append([]Project(nil), c.projects()...)
	for _, p := range items {
		if p.Path == path {
			return
		}
	}
	items = append([]Project{{Name: filepath.Base(path), Path: path}}, items...)
	c.setProjects(items)
	if home, err := os.UserHomeDir(); err == nil {
		configPath := filepath.Join(home, ".codex", "config.toml")
		if data, err := os.ReadFile(configPath); err == nil {
			escaped := strings.ReplaceAll(path, `"`, `\"`)
			section := "\n[projects.\"" + escaped + "\"]\ntrust_level = \"trusted\"\n"
			if !strings.Contains(string(data), "[projects.\""+escaped+"\"]") {
				_ = os.WriteFile(configPath, append(data, []byte(section)...), 0600)
			}
		}
	}
}

func (c *chatController) renameProject(path, name string) {
	if c.remote != nil {
		c.forward("renameProject", svcProject{Path: path, Name: name})
		return
	}
	items := append([]Project(nil), c.projects()...)
	for i := range items {
		if items[i].Path == path {
			items[i].Name = name
			c.setProjects(items)
			return
		}
	}
}

// removeThreadLocal removes a thread from the visible list and its persisted
// project cache. The Codex archive call is intentionally separate so the UI
// can also remove locally-created threads that have no server ID yet.
func (c *chatController) removeThreadLocal(id int, serverID string) {
	items := append([]Thread(nil), c.threads()...)
	filtered := items[:0]
	for _, item := range items {
		if item.ID == id || (serverID != "" && item.ServerID == serverID) {
			continue
		}
		filtered = append(filtered, item)
	}
	c.setThreads(filtered)
	if c.projectCache != nil && c.cwd != "" {
		cached := append([]Thread(nil), c.projectCache[c.cwd]...)
		out := cached[:0]
		for _, item := range cached {
			if item.ID == id || (serverID != "" && item.ServerID == serverID) {
				continue
			}
			out = append(out, item)
		}
		c.projectCache[c.cwd] = out
	}
	if c.active() == id {
		if len(filtered) > 0 {
			c.setActive(filtered[0].ID)
		} else {
			c.setActive(0)
		}
	}
	persistThreadListCache(c.projectCache)
	c.refreshProjectOrder()
}

func (c *chatController) archiveThread(t Thread) {
	if c.remote != nil {
		c.forward("archive", t.ID)
		return
	}
	backendOf(t).Archive(c, t)
}

func (c *chatController) archiveCodexThread(t Thread) {
	client := c.client
	if client == nil || t.ServerID == "" {
		c.removeThreadLocal(t.ID, t.ServerID)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
		defer cancel()
		err := client.Call(ctx, "thread/archive", map[string]string{"threadId": t.ServerID}, nil)
		c.dispatch(func() {
			if err != nil {
				c.setStatus("Unable to archive conversation: " + err.Error())
				return
			}
			c.removeThreadLocal(t.ID, t.ServerID)
		})
	}()
}

// deleteThread removes the local entry immediately. Codex currently exposes
// archive as the server-side removal operation, so synchronize that state when
// a server-backed thread is deleted as well.
func (c *chatController) deleteThread(t Thread) {
	if c.remote != nil {
		c.forward("delete", t.ID)
		return
	}
	backendOf(t).Delete(c, t)
}

func (c *chatController) deleteCodexThread(t Thread) {
	c.removeThreadLocal(t.ID, t.ServerID)
	if c.client == nil || t.ServerID == "" {
		return
	}
	client := c.client
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
		defer cancel()
		if err := client.Call(ctx, "thread/archive", map[string]string{"threadId": t.ServerID}, nil); err != nil {
			c.dispatch(func() { c.setStatus("Unable to delete conversation: " + err.Error()) })
		}
	}()
}

func (c *chatController) removeProject(path string, deleteConfig bool) {
	path, _ = filepath.Abs(path)
	items := append([]Project(nil), c.projects()...)
	projects := items[:0]
	for _, project := range items {
		if project.Path != path {
			projects = append(projects, project)
		}
	}
	c.setProjects(projects)
	delete(c.projectCache, path)
	persistThreadListCache(c.projectCache)
	if !deleteConfig {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	escaped := strings.ReplaceAll(path, `"`, `\"`)
	block := "\n[projects.\"" + escaped + "\"]\ntrust_level = \"trusted\"\n"
	updated := strings.Replace(string(data), block, "\n", 1)
	if updated != string(data) {
		_ = os.WriteFile(configPath, []byte(updated), 0600)
	}
}

func (c *chatController) archiveProject(path string) {
	if c.remote != nil {
		c.forward("archiveProject", svcProject{Path: path})
		return
	}
	for _, thread := range append([]Thread(nil), c.projectCache[path]...) {
		c.archiveThread(thread)
	}
	c.removeProject(path, false)
}

func (c *chatController) deleteProject(path string) {
	if c.remote != nil {
		c.forward("deleteProject", svcProject{Path: path})
		return
	}
	for _, thread := range append([]Thread(nil), c.projectCache[path]...) {
		c.deleteThread(thread)
	}
	c.removeProject(path, true)
}

func (c *chatController) dispatch(f func()) {
	run := runOnMain
	if c.run != nil {
		run = c.run
	}
	run(func() {
		if !c.closed {
			f()
		}
	})
}
func (c *chatController) nextID() int { c.serial++; return c.serial }

// Keep published IDs across launches: older phones subscribe by this number.
// Repair invalid/duplicate cache entries above the highest restored ID.
func (c *chatController) assignCachedThreadIDs() {
	paths := make([]string, 0, len(c.projectCache))
	for path, threads := range c.projectCache {
		paths = append(paths, path)
		for _, t := range threads {
			c.serial = max(c.serial, t.ID)
		}
	}
	sort.Strings(paths)
	seen := map[int]bool{}
	for _, path := range paths {
		threads := c.projectCache[path]
		for i := range threads {
			if threads[i].ID <= 0 || seen[threads[i].ID] {
				threads[i].ID = c.nextID()
			}
			seen[threads[i].ID] = true
		}
		c.projectCache[path] = threads
	}
}
func (c *chatController) current() Thread {
	for _, t := range c.threads() {
		if t.ID == c.active() {
			return t
		}
	}
	return Thread{Title: "New thread", Project: filepath.Base(c.cwd)}
}

func (c *chatController) projectBusy(path string) bool {
	threads := c.projectCache[path]
	if path == c.cwd {
		threads = c.threads()
	} else if c.mirror != nil {
		return c.mirror.projectBusy[path]
	}
	for _, t := range threads {
		if t.Busy && !t.Loading {
			return true
		}
	}
	return false
}

func (c *chatController) update(id int, fn func(*Thread)) {
	items := append([]Thread(nil), c.threads()...)
	for i := range items {
		if items[i].ID == id {
			items[i].Messages = append([]Message(nil), items[i].Messages...)
			items[i].Tasks = append([]ThreadTask(nil), items[i].Tasks...)
			before := threadMetaOf(items[i])
			wasBusy := items[i].Busy
			fn(&items[i])
			c.setThreads(items)
			if c.projectCache != nil && c.cwd != "" {
				c.projectCache[c.cwd] = append([]Thread(nil), items...)
			}
			// Streamed text changes no metadata: reorder and write the sidebar's
			// cache only when what it keeps changed, not for every delta.
			if threadMetaOf(items[i]) != before {
				c.refreshProjectOrder()
				persistThreadListCache(c.projectCache)
			}
			if c.revision != nil {
				c.setRevision(c.revision() + 1)
			}
			if wasBusy && !items[i].Busy && len(items[i].Queue) > 0 {
				done := items[i].ID
				c.dispatch(func() { c.drainQueue(done) })
			}
			if len(c.handoffs) > 0 {
				c.refreshHandoffs(id)
			}
			return
		}
	}
	// Background projects retain live sessions and must receive stream and
	// asynchronous request results even while another project is selected.
	for path, cached := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for i := range cached {
			if cached[i].ID != id {
				continue
			}
			items := append([]Thread(nil), cached...)
			old := items[i]
			items[i].Messages = append([]Message(nil), old.Messages...)
			items[i].Tasks = append([]ThreadTask(nil), old.Tasks...)
			fn(&items[i])
			c.projectCache[path] = items
			if threadMetaOf(items[i]) != threadMetaOf(old) {
				c.refreshProjectOrder()
				persistThreadListCache(c.projectCache)
			}
			if c.revision != nil {
				c.setRevision(c.revision() + 1)
			}
			if n, ok := completionNotice(old, items[i]); ok {
				c.deliverNotice(n, "completion")
			}
			if old.Busy && !items[i].Busy && len(items[i].Queue) > 0 {
				c.dispatch(func() { c.drainQueue(id) })
			}
			return
		}
	}
}

func (c *chatController) refreshProjectOrder() {
	if c.projects == nil || c.setProjects == nil {
		return
	}
	projects := append([]Project(nil), c.projects()...)
	latest := func(path string) int64 {
		var result int64
		for _, t := range c.projectCache[path] {
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
		if project != c.projects()[i] {
			c.setProjects(projects)
			return
		}
	}
}
func (c *chatController) connect() {
	if c.remote != nil {
		c.forward("connect", nil)
		return
	}
	if c.connecting || c.ready() {
		return
	}
	c.resetCodexSessions()
	c.connecting = true
	c.connectionID++
	generation, cwd := c.connectionID, c.cwd
	if c.connectionCancel != nil {
		c.connectionCancel()
	}
	connectionCtx, cancel := context.WithCancel(c.ctx)
	c.connectionCancel = cancel
	c.setStatus("Connecting to Codex…")
	c.setHistoryStatus("Loading conversations…")
	go func() {
		ctx, done := context.WithTimeout(connectionCtx, 15*time.Second)
		client, err := codex.Start(ctx, "", cwd, codexMCPArgs(cwd)...)
		done()
		if err != nil {
			c.dispatch(func() {
				if c.connectionID != generation {
					return
				}
				c.connecting = false
				c.setStatus(err.Error())
				c.setHistoryStatus("Unable to load conversations: " + err.Error() + ". Reconnect to retry.")
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
		c.dispatch(func() {
			if c.connectionID != generation {
				go client.Close()
				return
			}
			c.client = client
			c.connecting = false
			c.setReady(true)
			c.setStatus("Connected · Codex app-server")
			c.quotaLimits = accountLimits{}
			c.refreshQuota()
			c.loadThreadList(connectionCtx, client, c.cwd, generation)
			c.loadModels(connectionCtx, client, generation)
		})
		for frame := range client.Events() {
			f := frame
			c.dispatch(func() {
				if c.client == client && c.connectionID == generation {
					c.handle(f)
				}
			})
		}
		c.dispatch(func() {
			if c.client != client || c.connectionID != generation {
				return
			}
			c.setReady(false)
			c.setApprovals(nil)
			c.setStatus(client.Err().Error())
			c.setHistoryStatus("Connection lost. Reconnect to retry.")
			c.resetCodexSessions()
		})
	}()
}

func (c *chatController) loadThreadList(ctx context.Context, client *codex.Client, cwd string, generation int) {
	projectPaths := []string{cwd}
	for _, project := range c.projects() {
		if project.Path != "" && project.Path != cwd {
			projectPaths = append(projectPaths, project.Path)
		}
	}
	watchdog := time.AfterFunc(20*time.Second, func() {
		c.dispatch(func() {
			if c.client == client && c.connectionID == generation && c.historyStatus() == "Loading conversations…" {
				c.setHistoryStatus("History request timed out. Reconnect to retry.")
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
		c.dispatch(func() {
			if c.client != client || c.connectionID != generation {
				return
			}
			received++
			if result.err != nil && firstErr == nil {
				firstErr = result.err
			}
			if c.projectCache == nil {
				c.projectCache = make(map[string][]Thread)
			}

			// Merge this project as soon as its own request completes. The active
			// project is published immediately; slower projects append later.
			projectPath, _ := filepath.Abs(result.cwd)
			items := c.projectCache[projectPath]
			if projectPath == c.cwd {
				items = append([]Thread(nil), c.threads()...)
				for _, local := range c.projectCache[projectPath] {
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
					if threadAgent(items[i]) == agentCodex && items[i].ServerID == remote.ID {
						found = true
						items[i].Title = conversationTitle(remote.Name, remote.Preview, items[i].Title)
						items[i].Age = time.Unix(remote.UpdatedAt, 0).Format("Jan 2")
						items[i].LastActivityAt = max(items[i].LastActivityAt, remote.UpdatedAt*1000)
						break
					}
				}
				if !found {
					items = append(items, Thread{ID: c.nextID(), ServerID: remote.ID, Title: conversationTitle(remote.Name, remote.Preview, ""), Project: filepath.Base(projectPath), Age: time.Unix(remote.UpdatedAt, 0).Format("Jan 2"), LastActivityAt: remote.UpdatedAt * 1000})
				}
			}
			if len(items) > maxVisibleThreads {
				items = items[:maxVisibleThreads]
			}
			c.projectCache[projectPath] = append([]Thread(nil), items...)
			if projectPath == c.cwd {
				c.setThreads(items)
			}
			if received == len(projectPaths) {
				c.refreshProjectOrder()
			}
			persistThreadListCache(c.projectCache)
			// Any successful project means the blocking loading state is over.
			if result.err == nil || len(result.data) > 0 {
				c.setHistoryStatus("")
			} else if received == len(projectPaths) && len(c.threads()) == 0 && firstErr != nil {
				c.setHistoryStatus("History unavailable: " + firstErr.Error())
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func (c *chatController) loadModels(ctx context.Context, client *codex.Client, generation int) {
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
		c.dispatch(func() {
			if c.client != client || c.connectionID != generation {
				return
			}
			c.setModels(models.Data)
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
			c.selectModel(selected)
		})
	}()
}
func (c *chatController) readHistory(t Thread) {
	c.resume(t, nil)
}

func isWriterConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already has an active writer") || strings.Contains(message, "already has a live local writer")
}

// changeWorkspace changes the selected project while keeping Codex sessions
// alive on the shared app-server. The native directory picker validates that
// the user selected a directory, but keep this check here as well because the
// controller is also used by tests and other callers.
func (c *chatController) changeWorkspace(path string) error {
	if c.remote != nil {
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
	if abs == c.cwd {
		return nil
	}

	if c.projectCache == nil {
		c.projectCache = make(map[string][]Thread)
	}
	cached := append([]Thread(nil), c.threads()...)
	for i := range cached {
		// A running conversation goes on in the background, as Codex's do;
		// its frames follow it to the project it stays in.
		if cached[i].Busy || c.claudeRuns[cached[i].ID] != nil {
			continue
		}
		if threadAgent(cached[i]) != agentCodex {
			backendOf(cached[i]).Suspend(c, cached[i])
			cached[i].Loaded = false
			cached[i].Loading = false
			cached[i].Busy = false
			cached[i].TurnID = ""
		}
	}
	c.projectCache[c.cwd] = cached
	c.cwd = abs
	c.rememberProject(abs)
	cachedThreads := append([]Thread(nil), c.projectCache[abs]...)
	c.setThreads(cachedThreads)
	if len(cachedThreads) > 0 {
		c.setHistoryStatus("")
	} else {
		c.setHistoryStatus("Loading conversations…")
	}
	c.setActive(0)
	c.setDraft("")
	if c.ready() && c.client != nil {
		c.loadThreadList(c.ctx, c.client, abs, c.connectionID)
	}
	c.discoverAll()
	return nil
}
func (c *chatController) selectModel(value string) {
	if c.remote != nil {
		c.setModel(value)
		c.forward("model", value)
		return
	}
	c.setModel(value)
	c.setEffort("")
	for _, m := range c.models() {
		if m.Model == value {
			c.setEffort(savedCodexEffort(m))
			return
		}
	}
}
func (c *chatController) modelNames() []string {
	out := []string{}
	for _, m := range c.models() {
		out = append(out, m.Model)
	}
	if c.model() != "" {
		found := false
		for _, s := range out {
			if s == c.model() {
				found = true
			}
		}
		if !found {
			out = append(out, c.model())
		}
	}
	return out
}
func (c *chatController) efforts() []string {
	for _, m := range c.models() {
		if m.Model == c.model() {
			out := []string{}
			for _, e := range m.SupportedReasoningEfforts {
				out = append(out, e.ReasoningEffort)
			}
			return out
		}
	}
	return nil
}
func (c *chatController) newThread() {
	if c.setDraftPermission != nil {
		c.setDraftPermission("")
	}
	c.setActive(0)
	c.setDraft("")
	c.setAttachments(nil)
}
func (c *chatController) rememberDraft(value string) {
	c.setDraft(value)
	if c.remote != nil {
		if c.active() != 0 {
			if c.mirror != nil {
				if c.mirror.drafts == nil {
					c.mirror.drafts = map[int]string{}
				}
				c.mirror.drafts[c.active()] = value
			}
			c.forward("draft", svcThreadValue{Thread: c.active(), Value: value})
		}
		return
	}
	c.update(c.active(), func(t *Thread) { t.Draft = value })
}

func (c *chatController) addAttachments(paths []string) {
	items := append([]string(nil), c.attachments()...)
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
	c.setAttachments(items)
}

func (c *chatController) removeAttachment(path string) {
	items := make([]string, 0, len(c.attachments()))
	for _, item := range c.attachments() {
		if item != path {
			items = append(items, item)
		}
	}
	c.setAttachments(items)
}

func (c *chatController) attachmentInputs(paths []string) []map[string]any {
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
func (c *chatController) selectThread(t Thread) {
	// Viewing history does not count as conversation activity.
	if latest := c.threadWithID(t.ID); latest.ID != 0 {
		t = latest
	}
	c.setActive(t.ID)
	draft := t.Draft
	if c.mirror != nil {
		if local, ok := c.mirror.drafts[t.ID]; ok {
			draft = local
		}
	}
	c.setDraft(draft)
	c.setAttachments(nil)
	if c.remote != nil {
		c.forward("open", t.ID)
		return
	}
	backendOf(t).Open(c, t)
}

// resume continues a Codex thread, then submits after, when given.
func (c *chatController) resume(t Thread, after *sendRequest) {
	if c.remote != nil {
		c.forward("resume", t.ID)
		return
	}
	client := c.client
	cwd := c.threadWorkspace(t.ID)
	policy := c.permissionForThread(t)
	if client == nil {
		c.update(t.ID, func(t *Thread) {
			t.Busy = false
			t.Loading = false
			t.Status = "Codex is reconnecting…"
		})
		if after != nil {
			c.returnDraft(t.ID, after.Text, after.Attachments)
		}
		return
	}
	c.update(t.ID, func(t *Thread) {
		t.Busy = true
		t.Loading = true
		t.Status = "Loading conversation…"
	})
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		defer cancel()
		var result codex.ThreadResponse
		err := client.Call(ctx, "thread/resume", map[string]any{"threadId": t.ServerID, "cwd": cwd, "sandbox": sandboxForPermissionPolicy(policy), "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy)}, &result)
		readOnly := isWriterConflict(err)
		if readOnly {
			// Another Codex process owns the writer. Read the persisted rollout through
			// thread/read so this client can still act as a conversation viewer.
			var readResult codex.ThreadResponse
			readErr := client.Call(ctx, "thread/read", map[string]any{"threadId": t.ServerID, "includeTurns": true}, &readResult)
			if readErr == nil {
				result = readResult
				err = nil
			}
		}
		// Decode attachments and normalize the complete rollout off the UI thread.
		// Runtime IDs are assigned only after returning to the UI owner.
		var messages []Message
		if err == nil {
			messages = prepareHistoryMessages(result.Thread)
		}

		c.dispatch(func() {
			if c.client != client {
				c.update(t.ID, func(t *Thread) {
					t.Loading = false
					t.Busy = false
					t.Status = "Connection changed while loading. Click to retry."
				})
				if after != nil {
					c.returnDraft(t.ID, after.Text, after.Attachments)
				}
				return
			}
			c.update(t.ID, func(t *Thread) {
				t.Busy = false
				t.Loading = false
				t.ReadOnly = readOnly
				t.Resumed = err == nil && !readOnly
				if err != nil {
					t.Status = err.Error()
					c.upsert(t, Message{Role: "error", Kind: "error", Status: "failed", Content: "Unable to load conversation: " + err.Error(), ItemID: "resume-error"})
					return
				}
				t.Loaded = true
				t.Resumed = !readOnly
				t.HistoryLoaded = true
				t.ReadOnly = readOnly
				if readOnly {
					t.Status = "只读 · 此对话正在其他窗口运行"
				} else {
					t.Status = ""
				}
				t.Title = conversationTitle(result.Thread.Name, result.Thread.Preview, t.Title)
				for i := range messages {
					messages[i].ID = c.nextID()
				}
				t.Messages = messages
				for _, turn := range result.Thread.Turns {
					if turn.Status == "inProgress" && !readOnly {
						t.Busy = true
						t.TurnID = turn.ID
						t.Status = "Working…"
					}
				}
			})
			if err == nil {
				c.refreshCodexGoal(t.ID)
			}
			if after != nil && (err != nil || readOnly || c.submit(*after) == 0) {
				c.returnDraft(t.ID, after.Text, after.Attachments)
			}
		})
	}()
}
func (c *chatController) historyMessages(remote codex.Thread) []Message {
	out := prepareHistoryMessages(remote)
	for i := range out {
		out[i].ID = c.nextID()
	}
	return out
}

// This conversion has no controller or reactive state and may run in a worker.
func prepareHistoryMessages(remote codex.Thread) []Message {
	var out []Message
	for _, turn := range remote.Turns {
		for _, item := range turn.Items {
			if m, ok := itemMessage(item); ok {
				if m.Status == "" {
					m.Status = turn.Status
				}
				m.ItemID = item.ID
				m.TurnID = turn.ID
				out = append(out, m)
			}
		}
		if turn.Error != nil {
			out = append(out, Message{Role: "error", Kind: "error", Status: "failed", Content: turn.Error.Message, TurnID: turn.ID, ItemID: "turn-error"})
		}
	}
	return out
}

// send submits the window's draft in its open conversation.
func (c *chatController) send() { c.sendDraft(false) }

// sendDraft submits the window's draft; in a running turn it steers the
// turn, or waits in the queue when steer is false.
func (c *chatController) sendDraft(steer bool) {
	req := sendRequest{Steer: steer, ThreadID: c.current().ID, Text: c.draft(), Attachments: append([]string(nil), c.attachments()...), Agent: c.currentAgent(), Model: c.currentBackend().Model(c), Effort: c.currentBackend().Effort(c), Policy: c.currentPermissionPolicy()}
	if id := c.submit(req); id != 0 {
		c.setActive(id)
		c.rememberDraft("")
		c.setAttachments(nil)
	}
}

func (c *chatController) sendCodex(req sendRequest) int {
	if !c.ready() {
		return 0
	}
	text, attachments := req.Text, req.Attachments
	t := c.threadWithID(req.ThreadID)
	if t.ServerID != "" && !t.Resumed {
		c.resume(t, &req)
		return t.ID
	}
	if t.ID == 0 {
		// Use the first request until the agent supplies a named title.
		project := firstNonEmpty(req.Project, c.cwd)
		t = Thread{PermissionPolicy: req.Policy, ID: c.nextID(), Title: conversationTitle("", text, ""), Project: filepath.Base(project), Age: "now", Loaded: true}
		c.addThread(project, t)
	}
	id := t.ID
	client := c.client
	if client == nil {
		c.update(id, func(t *Thread) {
			t.Busy = false
			t.Status = "Codex is reconnecting…"
		})
		return id
	}
	cwd := c.threadWorkspace(id)
	model, effort, policy := req.Model, req.Effort, req.Policy
	serviceTier := loadSetting("codexServiceTier")
	sentAt := time.Now().UnixMilli()
	c.update(id, func(t *Thread) {
		t.Busy = true
		t.LastActivityAt = sentAt
		t.Status = "Starting…"
		t.Draft = ""
		t.StopRequested = false
		content, images := attachmentDisplay(text, attachments)
		t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "user", Content: content, Images: images, ItemID: "pending-user", SentAtMs: sentAt})
	})
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 45*time.Second)
		defer cancel()
		serverID := t.ServerID
		var err error
		missingThread := false
		if serverID == "" {
			var r codex.ThreadResponse
			params := map[string]any{"cwd": cwd, "sandbox": sandboxForPermissionPolicy(policy), "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy)}
			if model != "" {
				params["model"] = model
			}
			err = client.Call(ctx, "thread/start", params, &r)
			if err == nil {
				serverID = r.Thread.ID
				if serverID == "" {
					err = fmt.Errorf("Codex returned an empty thread ID")
				} else {
					sid := serverID
					serverName := strings.TrimSpace(r.Thread.Name)
					c.dispatch(func() {
						c.update(id, func(t *Thread) {
							t.ServerID = sid
							t.Resumed = true
							if serverName != "" {
								t.Title = serverName
							}
						})
					})
				}
			}
		}
		var result struct {
			Turn codex.Turn `json:"turn"`
		}
		if err == nil {
			input := []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}
			input = append(input, fileMentionInputs(text, cwd)...)
			input = append(input, c.attachmentInputs(attachments)...)
			params := map[string]any{"threadId": serverID, "cwd": cwd, "input": input, "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy), "sandboxPolicy": turnSandboxForPermissionPolicy(policy)}
			if model != "" {
				params["model"] = model
			}
			if effort != "" {
				params["effort"] = effort
			}
			if serviceTier != "" {
				params["serviceTierForTurn"] = serviceTier
			}
			err = client.Call(ctx, "turn/start", params, &result)
			if isMissingCodexThread(err, serverID) {
				// The server explicitly rejected this turn before accepting input.
				// Restore the same thread and retry once, without another user bubble.
				missingThread = true
				err = client.Call(ctx, "thread/resume", map[string]any{"threadId": serverID, "cwd": cwd, "sandbox": sandboxForPermissionPolicy(policy), "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy)}, nil)
				if err == nil {
					err = client.Call(ctx, "turn/start", params, &result)
				}
			}
		}
		c.dispatch(func() {
			if c.client != client {
				return
			}
			if err != nil {
				c.update(id, func(t *Thread) {
					t.Busy = false
					if missingThread {
						t.Resumed = false
					}
					t.Status = "Send failed: " + err.Error()
					c.upsert(t, Message{Role: "error", Kind: "error", Status: "failed", Content: "Send failed: " + err.Error(), ItemID: "send-error-" + fmt.Sprint(c.nextID())})
					t.Draft = text
					t.Loaded = t.ServerID == ""
				})
				c.returnDraft(id, text, attachments)
				return
			}
			c.update(id, func(t *Thread) {
				if t.Busy {
					t.TurnID = result.Turn.ID
					t.Status = "Working…"
				}
			})
			if t := c.threadWithID(id); t.StopRequested && t.Busy {
				c.interrupt(t)
			}
		})
	}()
	return id
}

func (c *chatController) stop() { c.stopThread(c.current().ID) }

func (c *chatController) stopThread(id int) {
	if c.remote != nil {
		c.forward("stop", id)
		return
	}
	t := c.threadWithID(id)
	if !t.Busy && c.claudeRuns[t.ID] == nil {
		return
	}
	c.update(t.ID, func(t *Thread) { t.StopRequested = true; t.Status = "Stopping…" })
	backendOf(t).Stop(c, t)
}
func (c *chatController) interrupt(t Thread) {
	client := c.client
	if client == nil {
		c.update(t.ID, func(t *Thread) { t.StopRequested = false; t.Status = "Codex is reconnecting…" })
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
		defer cancel()
		err := client.Call(ctx, "turn/interrupt", map[string]string{"threadId": t.ServerID, "turnId": t.TurnID}, nil)
		if err != nil {
			c.dispatch(func() { c.update(t.ID, func(t *Thread) { t.Status = "Stop failed: " + err.Error() }) })
		}
	}()
}

// "on-failure" is retained as the stored ID for the existing Help me approve
// preference; it must not be sent as the protocol's deprecated failure policy.
func approvalForPermissionPolicy(policy string) string {
	if normalizePermissionPolicy(policy) == "never" {
		return "never"
	}
	return "on-request"
}
func reviewerForPermissionPolicy(policy string) string {
	if policy == "on-failure" {
		return "auto_review"
	}
	return "user"
}
func turnSandboxForPermissionPolicy(policy string) map[string]any {
	if policy == "never" {
		return map[string]any{"type": "dangerFullAccess"}
	}
	return map[string]any{"type": "workspaceWrite", "networkAccess": false}
}

func (c *chatController) permissionForThread(t Thread) string {
	if t.PermissionPolicy != "" {
		return normalizePermissionPolicy(t.PermissionPolicy)
	}
	return c.selectedPermissionPolicy()
}
func (c *chatController) currentPermissionPolicy() string {
	t := c.current()
	if t.ID == 0 && c.draftPermission != nil && c.draftPermission() != "" {
		return normalizePermissionPolicy(c.draftPermission())
	}
	return c.permissionForThread(t)
}
func (c *chatController) setCurrentPermissionPolicy(policy string) {
	policy = normalizePermissionPolicy(policy)
	if c.current().ID == 0 {
		if c.setDraftPermission != nil {
			c.setDraftPermission(policy)
		}
		return
	}
	if c.remote != nil {
		c.forward("threadPolicy", svcThreadValue{Thread: c.active(), Value: policy})
		return
	}
	c.update(c.active(), func(t *Thread) { t.PermissionPolicy = policy })
}
