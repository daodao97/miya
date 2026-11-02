package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/codex"
	"miya/internal/remote"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// The phone shows one desktop's projects and conversations, sends messages
// into them and answers their approvals. The desktop's background service
// runs the agents; the phone holds only what the desktop sent it.
type mobileApp struct {
	conversationMenu             bool
	conversationMenuProject      string
	conversationMenuAgent        string
	filesProxy                   *phoneFileProxy
	filesCancel                  context.CancelFunc
	fileRefs                     *mobileFileReferences
	approvalID                   json.RawMessage
	options                      map[string]remote.SendOptions
	optionsAgent                 string
	approvalPending              map[string]bool
	approvalFields               map[string]map[string]string
	readPending                  map[string]uint64
	badgeRevision                uint64
	badgeApplied                 int
	badgeUpdating                bool
	pushOpenRef                  remote.Request
	pushID, pushToken, pushError string
	pushDisabled, pushRequesting bool
	pushOpen                     int
	win                          *mygo.Window
	store                        mobileSecretStore
	desktops                     mobileDesktops
	desktopSessions              map[string]mobileDesktopSession
	desktopWrites                desktopPersistence
	desktopError                 string
	desktopNotice                string
	desktopLoadFailed            bool
	dialDesktop                  func(context.Context, string) (*remote.Conn, error)
	// view renders a conversation with the desktop's own message views, over
	// a controller that only holds what the desktop sent.
	view *chatApp
	nav  *ui.Router
	pal  mobilePalette
	dark bool
	// themed reports pal and the theme set for the current appearance.
	themed            bool
	textScale         float32
	instantNavigation bool

	loaded     bool
	link, host string
	state      phoneState
	memories   mobileMemory
	autos      mobileAutomations
	// selected is the phone's workspace for new conversations.
	selected           string
	expandedProject    string
	projectsExpanded   bool
	threadListExpanded map[string]bool
	drawerSearch       bool
	drawerQuery        string
	menu               bool
	quotaOpen          bool
	quotaAgent         string
	quotaDrawerShown   bool
	// drawerOffset is where the list page's row rests or is dragged to:
	// 0 closed, drawerW open.
	drawerOffset   ui.Point
	drawerW        float32
	drawerDragging bool
	// drawerVisible makes a tap on the list beside the drawer close it.
	drawerVisible bool
	// Last committed target, so each action gives feedback before animation.
	drawerFeedbackOpen bool
	// pressX is where the last press on the list page began, across it.
	pressX float32
	// rowPressed is whether the list page's row was pressed last frame.
	rowPressed bool
	// pressOpen is whether the drawer was open as that press began.
	pressOpen bool

	// The conversation page shows open, or a new conversation in newProject
	// when open is 0.
	open        int
	title       string
	newProject  string
	newAgent    string
	draft       string
	inlineInput inlineThreadInput
	pairInput   mobileTextField
	// images are photos to attach to the next message, as cache copies.
	images []string
	// previewPaths maps desktop image sources to phone-local thumbnails.
	// An empty value reserves an in-flight lookup or download.
	previewPaths   map[string]string
	previewRetries map[string]int
	// commands are Claude Code\'s slash commands, by project; asked
	// marks those requested on this connection.
	commands map[string][]composerChoice
	// msgCache keeps conversations\' messages by ServerID, most recent
	// last in msgKeys, so reopening one sends only what changed.
	msgCache map[string][]Message
	msgKeys  []string
	// dataDir is the app\'s user data; cacheDir the current desktop\'s
	// conversations on disk, and dirty what is yet to be written there.
	dataDir, cacheDir string
	cacheWork         mobileCacheWork
	dirty             map[string]any
	flushTimer        *time.Timer
	asked             map[string]bool
	sending           bool
	sendErr           string

	conn       *remote.Conn
	cancel     context.CancelFunc
	generation int
	connected  bool
	status     string
	retryAt    time.Time
	retryError string
	err        string
	scanning   bool
	paste      string
}

var errIncompatible = errors.New("桌面端版本不兼容，请把手机端和桌面端都更新到最新版")

// mobilePalette holds the phone's colors for one appearance.
type mobilePalette struct {
	bg, surface, ink, muted, line, bubble, ok, warn ui.Color
	// drawer stands out from the page behind its scrim.
	drawer ui.Color
}

var (
	lightPalette = mobilePalette{
		bg: ui.Hex("#ffffff"), surface: ui.Hex("#f2f2f4"), ink: ui.Hex("#141414"), muted: ui.Hex("#8a8a8f"),
		line: ui.Hex("#e3e3e6"), bubble: ui.Hex("#f2f2f4"), ok: ui.Hex("#30a46c"), warn: ui.Hex("#c27c0e"), drawer: ui.Hex("#ffffff"),
	}
	darkPalette = mobilePalette{
		bg: ui.Hex("#000000"), surface: ui.Hex("#1c1c1e"), ink: ui.Hex("#f2f2f2"), muted: ui.Hex("#8e8e93"),
		line: ui.Hex("#2c2c2e"), bubble: ui.Hex("#1c1c1e"), ok: ui.Hex("#3dd68c"), warn: ui.Hex("#f5a524"), drawer: ui.Hex("#000000"),
	}
)

// applyPalette follows the system appearance. The phone runs only this
// window, so the shared colors the message views read are set here too.
func (m *mobileApp) applyPalette(dark bool) {
	p, t := lightPalette, ui.LightTheme()
	if dark {
		p, t = darkPalette, ui.DarkTheme()
	}
	m.pal, m.dark, m.themed = p, dark, true
	canvas, side, panel, ink, muted, line = p.bg, p.bg, p.bg, p.ink, p.muted, p.line
	hover, chosen, bubble = p.surface, p.surface, p.bubble
	t.Background, t.Surface, t.SurfaceHover, t.Border = p.bg, p.surface, p.line, p.line
	t.Text, t.TextMuted, t.Accent, t.AccentText = p.ink, p.muted, p.ink, p.bg
	t.FontSize = m.font(17)
	m.view.theme = t
}

func agentName(agent string) string {
	if agent == agentClaude {
		return "Claude Code"
	}
	return "Codex"
}

// newViewerController holds a conversation for the message views without
// starting Codex or Claude Code.
func newViewerController() *chatController {
	c := &chatController{projectCache: map[string][]Thread{}}
	c.threads, c.setThreads = createSignal([]Thread{})

	c.active, c.setActive = createSignal(0)
	c.revision, c.setRevision = createSignal(0)
	c.draft, c.setDraft = createSignal("")
	c.attachments, c.setAttachments = createSignal([]string{})
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})

	c.status, c.setStatus = createSignal("")
	c.agent, c.setAgent = createSignal(agentCodex)
	// The shared views call these; left nil they would panic, as a Codex
	// message's rewind check once did with ready.
	c.ready, c.setReady = createSignal(false)
	c.projects, c.setProjects = createSignal([]Project{})
	c.model, c.setModel = createSignal("")
	c.effort, c.setEffort = createSignal("")
	c.models, c.setModels = createSignal([]codex.Model{})
	c.permissionPolicy, c.setPermissionPolicy = createSignal("")
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.locale, c.setLocale = createSignal("zh")
	c.historyStatus, c.setHistoryStatus = createSignal("")
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

func newMobileApp() *mobileApp {
	m := &mobileApp{badgeApplied: -1}
	m.view = &chatApp{
		c:          newViewerController(),
		theme:      appTheme(),
		mobile:     true,
		lists:      map[int]*ui.ListState{},
		copied:     map[int]bool{},
		open:       map[int]bool{},
		answers:    map[string]map[string]string{},
		forms:      map[string]string{},
		formErrors: map[string]string{},
	}
	m.view.md = markdown.Options{
		OnLink: func(link string) {
			if ui.SafeToOpen(link) {
				go mygo.Shell.OpenExternal(link)
			}
		},
		LoadImage: func(src string) (ui.ImageSource, error) { return loadImage(imageSourcePath(src)) },
	}
	m.view.imagePreview = m.messageImage
	m.view.onHandoff = m.handoffToNewThread
	m.view.onRetry = func(req retryRequest) {
		r := m.threadRequest("retry", req.ThreadID)
		r.MessageID = req.MessageID
		m.sendPhoneAction(r)
	}
	m.view.referenceThread = func(ref ThreadRef) (Thread, bool) {
		if ref.Source != m.state.ReferenceSource {
			return Thread{}, false
		}
		for _, p := range m.state.Projects {
			for _, t := range p.Threads {
				if t.ServerID == ref.SessionID && threadAgent(t) == ref.Backend {
					return t, true
				}
			}
		}
		return Thread{}, false
	}

	return m
}

func mobileMain() {
	m := newMobileApp()
	m.store, _ = mygo.NewSecureStore("desktop-connection", mygo.SecureStoreOptions{})
	m.pushDisabled = loadSetting("phone-notifications") == "off"
	mygo.App.OnOpenURL(func(raw string) {
		runOnMain(func() {
			if _, err := remote.ParseLink(raw); err == nil {
				m.connect(raw)
			}
		})
	})
	mygo.App.WhenReady(func() {
		m.win = mygo.NewWindow(mygo.WindowOptions{Title: "Miya", Width: 390, Height: 844, ExtendIntoSafeArea: true, Content: ui.View(m.viewRoot)})
		setAppWindow(m.win)
		m.setupPush()
		go m.loadSaved()

	})
	// iOS ends a suspended app's sockets; reconnect on return instead.
	// Write the conversations before iOS suspends the app, which may end it.
	mygo.App.OnDidEnterBackground(func() {
		runOnMain(func() {
			m.flushCache(true)
			m.disconnect()
		})
	})
	mygo.App.OnDidBecomeActive(func() { runOnMain(m.requestPush) })
	mygo.App.OnWillEnterForeground(func() {
		runOnMain(func() {
			if m.link != "" && m.cancel == nil {
				m.connect(m.link)
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// loadSaved reads the tunnel secret and the paired desktop off the main
// thread, as Keychain reads may block.
func (m *mobileApp) loadSaved() {
	var pushID string
	dataDir, _ := mygo.App.Path(mygo.PathUserData)
	if m.store != nil {
		if saved, err := m.store.Get("notification-device"); err == nil {
			pushID = string(saved)
		}
		if len(pushID) != 32 {
			var data [16]byte
			if _, err := rand.Read(data[:]); err == nil {
				pushID = fmt.Sprintf("%x", data[:])
				_ = m.store.Set("notification-device", []byte(pushID))
			}
		}
		secret, err := m.store.Get("tunnel-secret")
		if errors.Is(err, mygo.ErrSecretNotFound) || err == nil && len(secret) != 32 {
			secret = make([]byte, 32)
			rand.Read(secret)
			err = m.store.Set("tunnel-secret", secret)
		}
		if err == nil {
			remote.SetClientSecret(secret)
			if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
				remote.SetKeySlotFile(filepath.Join(dir, "client-key-slot"))
			}
		}
	}
	saved, migrated, desktopErr := readMobileDesktops(m.store)
	if migrated {
		if item, ok := saved.find(saved.Active); ok {
			migrateDesktopOptions(dataDir, item.Link)
		}
	}
	runOnMain(func() {
		m.loaded = true
		m.pushID = pushID
		m.dataDir = dataDir
		m.desktopLoadFailed = desktopErr != nil
		if desktopErr != nil {
			m.desktopError, m.err = desktopErr.Error(), desktopErr.Error()
			return
		}
		m.desktopError = ""
		m.restoreDesktops(saved)
		if item, ok := m.desktops.find(m.desktops.Active); ok && m.link == "" {
			m.connect(item.Link)
		}
		m.saveDesktops()
		m.requestPush()
	})
}

// onMain runs f on the main thread unless a newer connection replaced gen.
func (m *mobileApp) onMain(gen int, f func()) {
	runOnMain(func() {
		if m.generation == gen {
			f()
		}
	})
}

func (m *mobileApp) connect(link string) {
	if m.desktopLoadFailed {
		m.err = "请先重新读取已保存的桌面列表"
		return
	}
	item, err := m.desktops.remember(link, "")
	if err != nil {
		m.err = err.Error()
		return
	}
	link = item.Link
	changed := link != m.link
	if changed && m.link != "" {
		m.saveDesktopSession()
		m.persistState()
		m.flushCache(true)
	}
	m.readPending = nil
	m.approvalPending = nil
	m.disconnect()
	m.previewPaths = nil
	m.previewRetries = nil
	if changed {
		m.resetDesktopState()
	}
	m.desktops.Active = item.ID
	m.link, m.err, m.status = link, "", "正在连接桌面端…"
	m.openDiskCache(link)
	if m.host != "" {
		_, _ = m.desktops.remember(link, m.host)
	}
	if m.host == "" {
		m.host = item.Name
	}
	if changed {
		m.loadDesktopOptions()
		m.restoreDesktopSession(item.ID)
	}
	m.updateBadge()
	m.saveDesktops()
	m.asked = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.run(ctx, m.generation, link)
}

// disconnect ends the connection but keeps the paired desktop.
func (m *mobileApp) disconnect() {
	m.disconnectDesktop(false)
}

func (m *mobileApp) disconnectDesktop(unregister bool) {
	m.clearFileReferences()
	if !m.retryAt.IsZero() {
		m.status = ""
	}
	m.retryAt, m.retryError = time.Time{}, ""
	if m.filesCancel != nil {
		m.filesCancel()
		m.filesCancel = nil
	}
	if m.filesProxy != nil {
		m.filesProxy.Close()
		m.filesProxy = nil
	}
	m.generation++
	conn, cancel := m.conn, m.cancel
	m.cancel = nil
	if unregister && conn != nil {
		registration, _ := json.Marshal(pushRegistration{ID: m.pushID, Token: m.pushToken, Disabled: true})
		hasPushID := m.pushID != ""
		go func() {
			if hasPushID {
				_ = conn.Send(remote.Request{Op: "push", Push: registration})
			}
			if cancel != nil {
				cancel()
			}
			conn.Close()
		}()
	} else {
		if cancel != nil {
			cancel()
		}
		if conn != nil {
			go conn.Close()
		}
	}
	m.conn, m.connected = nil, false
	m.autos.loading = false
	if m.sending {
		m.sending, m.sendErr = false, "连接已断开，消息可能没有发出"
	}
}

// forget unpairs the desktop.
func (m *mobileApp) forget() {
	m.removeDesktop(m.currentDesktopID())
}

func (m *mobileApp) run(ctx context.Context, gen int, link string) {
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			m.onMain(gen, func() { m.retryAt, m.retryError = time.Time{}, ""; m.status = "正在重新连接桌面端…" })
		}
		dial := m.dialDesktop
		if dial == nil {
			dial = remote.Connect
		}
		conn, err := dial(ctx, link)
		if err == nil {
			if ctx.Err() != nil {
				conn.Close()
				return
			}
			var greeted bool
			greeted, err = m.read(gen, conn)
			conn.Close()
			if greeted {
				attempt = 0
			}
		}
		if ctx.Err() != nil {
			return
		}
		delay := min(time.Second<<min(attempt, 4), 15*time.Second)
		if errors.Is(err, errIncompatible) {
			delay = 15 * time.Second
		}
		message := fmt.Sprintf("连接已断开，%d 秒后重试", int(delay.Seconds()))
		if err != nil {
			message += "：" + err.Error()
		}
		retryAt := time.Now().Add(delay)
		retryError := ""
		if err != nil {
			retryError = err.Error()
		}
		m.onMain(gen, func() {
			m.conn, m.connected, m.status = nil, false, message
			m.retryAt, m.retryError = retryAt, retryError
			if m.filesProxy != nil {
				m.filesProxy.setConn(nil)
			}
			if m.sending {
				m.sending, m.sendErr = false, "连接已断开，消息可能没有发出"
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// read delivers the desktop's frames until the connection ends. greeted
// reports whether the desktop answered, so a dropped link retries promptly.
func (m *mobileApp) read(gen int, conn *remote.Conn) (greeted bool, err error) {
	for {
		// The desktop sends at least a ping every 15 seconds.
		conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		var f remote.Frame
		if err := conn.Receive(&f); err != nil {
			return greeted, err
		}
		switch f.Type {
		case "hello":
			if f.Version != remote.ProtocolVersion {
				return greeted, errIncompatible
			}
			greeted = true
			if err := conn.EnableAttachments(f.AttachmentToken); err != nil {
				return greeted, err
			}
			// The desktop compresses everything after hello.
			conn.DecompressReceives()
			host := f.Host
			m.onMain(gen, func() {
				m.host, m.connected, m.status, m.err = host, true, "", ""
				m.retryAt, m.retryError = time.Time{}, ""
				_, _ = m.desktops.remember(m.link, host)
				m.conn = conn
				m.autos.loading, m.autos.requestedAt = false, time.Time{}
				m.memories.loading, m.memories.requestedAt = false, time.Time{}
				if m.filesProxy != nil {
					m.filesProxy.setConn(conn)
				}
				m.previewPaths = nil // Retry previews requested while offline.
				m.previewRetries = nil
				if open := m.open; open != 0 {
					r := m.subscribeRequest(open)
					go conn.Send(r)
				}
				m.badgeRevision = 0
				m.syncPush()
				m.saveDesktops()
			})
		case "memory":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.receiveMemory(data, message) })
		case "automations":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.receiveAutomations(data, message) })
		case "action":
			message := f.Error
			m.onMain(gen, func() {
				if message != "" {
					m.sendErr = message
				}
			})
		case "responded":
			var result phoneApprovalReply
			if json.Unmarshal(f.Data, &result) == nil {
				message := f.Error
				m.onMain(gen, func() { m.approvalAnswered(result.ID, message) })
			}
		case "read":
			var n phoneNotificationState
			if json.Unmarshal(f.Data, &n) == nil {
				m.onMain(gen, func() { m.applyNotifications(n) })
			}
		case "push":
			message := f.Error
			m.onMain(gen, func() { m.pushError = message; m.readPending = nil; m.markOpenRead() })
		case "state":
			var s phoneState
			if json.Unmarshal(f.Data, &s) == nil {
				m.onMain(gen, func() {
					m.applyPhoneState(s)
				})
			}
		case "thread":
			var d phoneThreadDelta
			if json.Unmarshal(f.Data, &d) == nil {
				id := f.Thread
				m.onMain(gen, func() {
					m.receiveThread(id, d)
				})
			}
		case "image":
			var preview phoneImagePreview
			if json.Unmarshal(f.Data, &preview) == nil {
				if f.Error != "" {
					m.onMain(gen, func() { m.retryImage(preview.Source, conn) })
					continue
				}
				if f.Attachment != "" {
					id := f.Attachment
					go func() {
						data, err := conn.ReadAttachment(conn.Context(), id)
						if err != nil {
							m.onMain(gen, func() { m.retryImage(preview.Source, conn) })
							return
						}
						preview.Data = data
						m.onMain(gen, func() {
							if m.conn == conn {
								m.receiveImage(preview)
							}
						})
					}()
				} else {
					m.onMain(gen, func() { m.receiveImage(preview) })
				}
			}
		case "commands":
			var pc phoneCommands
			if json.Unmarshal(f.Data, &pc) == nil {
				m.onMain(gen, func() {
					if m.commands == nil {
						m.commands = map[string][]composerChoice{}
					}
					if pc.Commands == nil {
						pc.Commands = []composerChoice{}
					}
					m.commands[pc.Project] = pc.Commands
				})
			}
		case "sent":
			id, msg := f.Thread, f.Error
			m.onMain(gen, func() { m.sent(id, msg) })
		}
	}
}

// The desktop may renumber threads after a restart or while repairing an old
// cache. Rebind the open page by durable identity before handling its deltas.
func (m *mobileApp) applyPhoneState(s phoneState) {
	if m.state.MemoryRevision != s.MemoryRevision {
		m.memories.list = memoryList{}
		m.memories.requestedAt = time.Time{}
	}
	for _, old := range m.state.Approvals {
		same := false
		for _, next := range s.Approvals {
			if sameJSON(old.ID, next.ID) && approvalFingerprint(old) == approvalFingerprint(next) {
				same = true
				break
			}
		}
		if !same {
			id := string(old.ID)
			delete(m.approvalFields, id)
			delete(m.approvalPending, id)
			delete(m.view.formErrors, id)
		}
	}
	cur := m.view.c.current()
	if cur.ServerID == "" && m.open != 0 {
		cur, _ = m.find(m.open)
	}
	notifications := m.state.Notifications
	m.state = s
	m.state.Notifications = notifications
	m.applyNotifications(s.Notifications)
	if m.open != 0 && cur.ServerID != "" {
		for _, p := range s.Projects {
			for _, t := range p.Threads {
				if t.ServerID == cur.ServerID && threadAgent(t) == threadAgent(cur) && t.ID != m.open {
					m.open, cur.ID = t.ID, t.ID
					m.showThread(cur)
					m.subscribe(t.ID)
				}
			}
		}
	}
	m.openPushThread()
	m.markOpenRead()
	for id := range m.approvalFields {
		live := false
		for _, a := range s.Approvals {
			if string(a.ID) == id {
				live = true
				break
			}
		}
		if !live {
			delete(m.approvalFields, id)
			delete(m.approvalPending, id)
			delete(m.view.formErrors, id)
		}
	}
	m.view.c.setApprovals(s.Approvals)
	m.persistState()
}

func (m *mobileApp) receiveThread(id int, d phoneThreadDelta) {
	if m.open != id {
		return
	}
	cur := m.view.c.current()
	if cur.ID == id && cur.ServerID != "" {
		if d.Thread.ServerID == "" || (d.Thread.ServerID == cur.ServerID && (d.Thread.Loading || !d.Thread.HistoryLoaded) && len(d.Order) == 0) {
			// A loading or missing-thread response is not an empty history.
			// Keep the displayed messages and cache until history arrives,
			// including responses from desktops that still send an empty order.
			cur.Status, cur.Loading = d.Thread.Status, d.Thread.Loading
			m.updateThreadView(cur, false)
			return
		}
		if d.Thread.ServerID != cur.ServerID || threadAgent(d.Thread) != threadAgent(cur) {
			return
		}
	}
	d.Thread.ID = id
	m.applyThread(d)
}

func (m *mobileApp) showThread(t Thread) {
	m.updateThreadView(t, true)
}

func (m *mobileApp) updateThreadView(t Thread, messagesChanged bool) {
	v := m.view.c
	v.setThreads([]Thread{t})
	v.setActive(t.ID)
	if messagesChanged {
		v.setRevision(v.revision() + 1)
	}
	if t.Title != "" {
		m.title = t.Title
	}
}

func (m *mobileApp) subscribe(id int) {
	if conn := m.conn; conn != nil {
		r := m.subscribeRequest(id)
		go conn.Send(r)
	}
}

// find looks a conversation up in the desktop's state.
func (m *mobileApp) find(id int) (Thread, phoneProject) {
	for _, p := range m.state.Projects {
		for _, t := range p.Threads {
			if t.ID == id {
				return t, p
			}
		}
	}
	return Thread{}, phoneProject{}
}

func (m *mobileApp) project(path string) phoneProject {
	for _, p := range m.state.Projects {
		if p.Path == path {
			return p
		}
	}
	return phoneProject{Path: path, Name: filepath.Base(path)}
}

// currentProject is the desktop's open project, where a new conversation
// starts.
func (m *mobileApp) currentProject() phoneProject {
	for _, p := range m.state.Projects {
		if p.Current {
			return p
		}
	}
	if len(m.state.Projects) > 0 {
		return m.state.Projects[0]
	}
	return phoneProject{}
}

func (m *mobileApp) approvalsFor(serverID string) []codex.Approval {
	if serverID == "" {
		return nil
	}
	var out []codex.Approval
	for _, a := range m.state.Approvals {
		if a.ThreadID == serverID {
			out = append(out, a)
		}
	}
	return out
}

func (m *mobileApp) resetComposer() {
	m.clearImages()
	m.draft, m.sendErr, m.sending = "", "", false
}

func (m *mobileApp) openThread(t Thread) {
	m.drawerSearch, m.drawerQuery = false, ""
	if _, project := m.find(t.ID); project.Path != "" {
		m.selected, m.expandedProject, m.projectsExpanded = project.Path, project.Path, true
	}
	if m.open == t.ID && m.router().Path() == "/thread" {
		m.menu = false
		return
	}
	m.open, m.title, m.newProject, m.menu = t.ID, t.Title, "", false
	m.resetComposer()
	// Show what the phone holds at once; the desktop sends what changed.
	if cached := m.cachedMessages(t.ServerID); len(cached) > 0 {
		t.Messages, t.HistoryLoaded = cached, true
	} else {
		t.Loading = true
	}
	m.showThread(t)
	m.subscribe(t.ID)
	m.openDrawerPage("/thread")
	m.markOpenRead()
}

// leave closes the conversation page's state, after the router went back.
func (m *mobileApp) leave() {
	m.open, m.newProject = 0, ""
	m.view.c.setActive(0)
	m.subscribe(0)
	m.resetComposer()
}

// home returns to the new-conversation page at once.
func (m *mobileApp) home() {
	if m.nav != nil && m.nav.Path() != "/" {
		// Resetting/replacing a draft reuses one controller; there is no
		// independent outgoing conversation to animate in this case.
		m.instantNavigation = true
	}
	m.router().Reset("/")
	m.leave()
}

func (m *mobileApp) router() *ui.Router {
	if m.nav == nil {
		m.nav = ui.NewRouter("/")
		m.nav.Transition = ui.TransitionStack
		m.nav.Animation = &ui.Animation{Duration: 280 * time.Millisecond, Ease: ui.EaseOut}
	}
	return m.nav
}

func (m *mobileApp) send() { m.sendDraft(false) }

func (m *mobileApp) sendDraft(steer bool) {
	if m.sending {
		return
	}
	conn := m.conn
	if conn == nil {
		m.sendErr = "未连接桌面端"
		return
	}
	if m.open == 0 && m.newProject == "" {
		project := m.selectedProject()
		if project.Path == "" {
			m.sendErr = "请先在桌面端添加项目"
			return
		}
		m.newProject, m.newAgent = project.Path, m.composerAgent()
		m.router().Push("/thread")
	}
	r := m.threadRequest("send", m.open)
	r.Text = strings.TrimSpace(m.draft)
	r.Steer = steer
	agent := m.composerAgent()
	if m.open != 0 {
		agent = threadAgent(m.view.c.current())
	}
	if len(m.state.Agents) > 0 {
		o := m.sendOptions(agent)
		r.Options = &o
	}
	if m.open == 0 {
		r.Project, r.Agent = m.newProject, m.newAgent
	}
	m.sending, m.sendErr = true, ""
	gen := m.generation
	paths := append([]string(nil), m.images...)
	go func() {
		images, err := readPhoneImages(paths)
		if err == nil {
			r.Images = images
			err = conn.Send(r)
		}
		if err != nil {
			m.onMain(gen, func() {
				if m.sending {
					m.sending, m.sendErr = false, "发送失败："+err.Error()
				}
			})
		}
	}()
}

// sent applies the desktop's answer to a send.
func (m *mobileApp) sent(id int, msg string) {
	if !m.sending {
		return
	}
	m.sending = false
	if msg != "" {
		m.sendErr = msg
		mobileFeedback(mygo.HapticError)
		return
	}
	mobileFeedback(mygo.HapticLight)
	m.draft, m.sendErr = "", ""
	m.clearImages()
	if m.open == 0 && m.newProject != "" && id != 0 {
		m.open, m.newProject = id, ""
		m.showThread(Thread{ID: id, Agent: m.newAgent, Loading: true})
		m.subscribe(id)
	}
}

func (m *mobileApp) stop() {
	if conn, id := m.conn, m.open; conn != nil && id != 0 {
		r := m.threadRequest("stop", id)
		go conn.Send(r)
	}
}

func (m *mobileApp) scan() {
	if m.scanning {
		return
	}
	m.scanning, m.err = true, ""
	go func() {
		raw, err := mygo.Scanner.ScanCode(context.Background(), mygo.ScanOptions{Prompt: "扫描 Miya 桌面端的二维码", CancelLabel: "取消"})
		runOnMain(func() {
			m.scanning = false
			if err != nil {
				m.err = scanError(err)
			} else if raw != "" {
				m.pair(raw)
			}
		})
	}()
}

func (m *mobileApp) pair(raw string) {
	if _, err := remote.ParseLink(raw); err != nil {
		m.err = err.Error()
		return
	}
	m.paste = ""
	m.connect(raw)
}

// scanError explains a failed scan; closing the scanner is not a failure.
func scanError(err error) string {
	switch {
	case errors.Is(err, mygo.ErrScanCanceled):
		return ""
	case errors.Is(err, mygo.ErrUnsupported):
		return "请在 iPhone 上扫码，或粘贴桌面端连接码"
	case errors.Is(err, mygo.ErrCameraDenied):
		return "请在系统设置中允许 Miya 使用相机，或粘贴连接码"
	case errors.Is(err, mygo.ErrCameraUnavailable):
		return "相机不可用，请粘贴桌面端连接码"
	}
	return "暂时无法打开相机"
}

func (m *mobileApp) connectionLine() string {
	switch {
	case m.err != "":
		return m.err
	case m.status != "":
		return m.status
	case !m.connected:
		return "未连接"
	}
	return ""
}

func (m *mobileApp) viewRoot(c *ui.Context) {
	m.updateReconnectCountdown(c)
	m.textScale = c.Preferences().TextScale
	// Before SetTheme, the theme is the system's appearance.
	if dark := c.Theme().Dark; !m.themed || dark != m.dark {
		m.applyPalette(dark)
	}
	m.view.theme.FontSize = m.font(17)
	c.SetTheme(m.view.theme)
	p := m.pal
	// Honor any safe-area padding supplied by the host.
	safe := c.SafeArea()
	pageColor := p.bg
	contentSafe := safe
	if m.loaded && m.link != "" {
		if m.router().Path() == "/" {
			pageColor = m.homeBackground()
		}
		// Keep the router's bounds fixed throughout navigation. Each page
		// owns its safe-area padding, including the outgoing page, so a
		// history change cannot resize or vertically shift the list.
		contentSafe = ui.SafeArea{}
	}
	// MyGo uses the opaque root fill for the surrounding iOS safe area,
	// including the status bar and home indicator.
	c.Root().Background(pageColor)
	ui.Column(c).Fill().Padding(contentSafe.Top, contentSafe.Right, contentSafe.Bottom, contentSafe.Left).Background(pageColor).TextColor(p.ink).FontSize(m.font(15)).Children(func() {
		switch {
		case !m.loaded:
			ui.Spinner(c).Margin(ui.Auto)
		case m.link == "":
			m.pairView(c)
		default:
			// Drawer destinations share the edge gesture; child pages pop.
			m.router().InteractiveBack = !mobilePageHasDrawer(m.router().Path())
			transition := m.router().Transition
			instant := m.instantNavigation
			m.instantNavigation = false
			if instant {
				m.router().Transition = ui.TransitionNone
			}
			m.router().View(c, func(page *ui.Route) {
				page.Page().Background(p.bg).Padding(safe.Top, safe.Right, safe.Bottom, safe.Left)
				if mobilePageHasDrawer(page.Path()) {
					page.Page().Padding(0)
					m.listPage(c, page.Path())
					return
				}
				switch page.Path() {
				case "/automation":
					m.automationsPage(c, true)
				case "/options":
					m.optionsView(c)
				case "/approval":
					m.approvalPage(c)
				case "/desktop/add":
					m.addDesktopView(c)
				default:
					page.Page().Padding(0)
					m.listPage(c, "/")
				}
			})
			m.router().Transition = transition
		}
	})
	m.conversationActionsSheet(c)
	// Release the conversation after navigation reaches the new-conversation page.
	if m.nav != nil && m.nav.Path() == "/" && !m.nav.Transitioning() && (m.open != 0 || m.newProject != "") {
		m.leave()
		c.Invalidate()
	}
}

// Derive the label from the retry deadline so a delayed frame or a return
// from the background catches up immediately, instead of counting ticks.
func (m *mobileApp) updateReconnectCountdown(c *ui.Context) {
	if m.retryAt.IsZero() {
		return
	}
	remaining := m.retryAt.Sub(c.Now())
	if remaining <= 0 {
		m.status = "正在重新连接桌面端…"
		return
	}
	seconds := int((remaining + time.Second - 1) / time.Second)
	m.status = fmt.Sprintf("连接已断开，%d 秒后重试", seconds)
	if m.retryError != "" {
		m.status += "：" + m.retryError
	}
	c.After(remaining - time.Duration(seconds-1)*time.Second)
}

// circle is a round floating button, as the header's.
func (m *mobileApp) circle(c *ui.Context, key, label string, child func()) ui.Element {
	b := m.phoneButton(c.Key(key)).Size(44, 44).Padding(0).Radius(22).Background(m.pal.surface).Justify(ui.Center).Label(label)
	b.Children(child)
	return b
}

// pill is a rounded text button; primary fills it with the ink color.
func (m *mobileApp) pill(c *ui.Context, key, label string, primary bool) ui.Element {
	bg, fg := m.pal.surface, m.pal.ink
	if primary {
		bg, fg = m.pal.ink, m.pal.bg
	}
	b := m.phoneButton(c.Key(key)).MinHeight(44).Padding(10, 16).Radius(22).Background(bg).Justify(ui.Center).Label(label)
	b.Children(func() { ui.Text(c, label).FontSize(m.font(14)).FontWeight(500).TextColor(fg) })
	return b
}

func (m *mobileApp) menuIcon(c *ui.Context) {
	ui.Column(c).Gap(4).Children(func() {
		for i := range 3 {
			ui.Box(c.Key(i)).Size(18, 2).Radius(1).Background(m.pal.ink)
		}
	})
}

func (m *mobileApp) pairView(c *ui.Context) {
	p := m.pal
	ui.Column(c).Grow(1).Justify(ui.Center).Gap(14).Padding(32).Children(func() {
		ui.Row(c).Size(64, 64).Radius(18).Background(p.surface).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			icon(c, "terminal-2", 30, p.ink)
		})
		ui.Text(c, "连接 Miya 桌面端").FontSize(m.font(26)).FontWeight(700).Margin(10, 0, 0, 0)
		ui.Text(c, "打开 Miya 桌面端，进入「设置 → Phone」开启手机连接，再扫描二维码。").FontSize(m.font(15)).TextColor(p.muted).LineHeight(1.5)
		scan := m.phoneButton(c.Key("scan")).FillWidth().MinHeight(52).Radius(16).Background(p.ink).Justify(ui.Center).Gap(8).Label("扫码连接")
		scan.Children(func() {
			if m.scanning {
				ui.Spinner(c).TextColor(p.bg)
			}
			ui.Text(c, "扫码连接").FontSize(m.font(16)).FontWeight(600).TextColor(p.bg)
		})
		if scan.Clicked() {
			m.scan()
		}
		ui.Row(c).FillWidth().MinHeight(52).Padding(0, 6, 0, 16).Gap(8).AlignItems(ui.Center).Background(p.surface).Radius(16).Children(func() {
			input := ui.TextInput(c.Key("paste"), &m.paste).Label("桌面端连接码").Placeholder("或粘贴连接码").Grow(1).MinWidth(0).Background(p.surface).Border(0, p.surface).
				InputOptions(ui.InputOptions{Keyboard: ui.KeyboardASCII, Correction: ui.CorrectionOff, Capitalization: ui.CapitalizeNone, Return: ui.ReturnGo})
			m.pairInput.restore(input)
			if m.pill(c, "paste-code", "粘贴", false).KeepFocus().Clicked() {
				m.pairInput.paste(c, input, &m.paste, false)
			}
			if m.pill(c, "paste-connect", "连接", false).Background(p.bg).Clicked() {
				m.pair(m.paste)
			}
		})
		if m.err != "" {
			ui.Text(c, m.err).TextColor(danger).FontSize(m.font(14))
		}
		if m.desktopLoadFailed {
			if m.pill(c, "reload-desktops", "重新读取桌面列表", false).Clicked() {
				go m.loadSaved()
			}
		}
	})
}

func (m *mobileApp) listView(c *ui.Context) {
	p := m.pal
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(12).AlignItems(ui.Center).Children(func() {
		if m.homeAction(c, "menu", "桌面端连接", "layout-sidebar").Clicked() {
			m.menu = !m.drawerVisible
		}
		ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(3).Children(func() {
			ui.Text(c, "Miya").FontSize(m.font(16)).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				dot, label := p.muted, m.connectionLine()
				if label == "" {
					dot, label = p.ok, m.host
					if label == "" {
						label = "已连接"
					}
				}
				ui.Box(c.Key("dot")).Size(6, 6).Radius(3).Background(dot)
				ui.Text(c, strings.TrimSuffix(label, ".local")).FontSize(m.font(11)).TextColor(p.muted).SingleLine().Ellipsis("…")
			})
		})
		if m.homeAction(c, "new-conversation", "新建对话", "edit").Clicked() {
			m.startConversation(m.selectedProject().Path, m.composerAgent())
		}
	})
	if len(m.state.Projects) == 0 {
		ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if m.connected {
				ui.Text(c, "桌面端还没有项目").TextColor(p.muted)
			} else {
				ui.Spinner(c)
			}
		})
		return
	}
	m.newConversationView(c, m.selectedProject())
	m.composer(c, m.composerAgent(), false)
}

// selectedProject is where a new conversation starts: the chosen project,
// else the desktop's open project.
func (m *mobileApp) selectedProject() phoneProject {
	if m.selected != "" {
		for _, p := range m.state.Projects {
			if p.Path == m.selected {
				return p
			}
		}
	}
	return m.currentProject()
}

// listPage puts the drawer beside the list, off screen to its left. The
// row slides right as a finger pulls it from the left edge or the menu
// opens it, pushing the list aside as iOS apps' sidebars do; letting go
// settles it open or closed by where it is and how fast it was thrown.
//
// A clickable element takes the presses on it, so a drag starting on one
// would not reach the row: the edge and the scrim are plain areas that let
// drags through, as the drawer's empty space does.
func (m *mobileApp) listPage(c *ui.Context, path string) {
	p := m.pal
	safe := c.SafeArea()
	screen := c.Root().Bounds().W
	w := min(float32(320), screen*0.84)
	m.drawerW = w
	if m.link == "" {
		m.menu = false
	}
	want := float32(0)
	if m.menu {
		want = w
	}
	if !m.drawerDragging && m.drawerOffset.X != want {
		// The menu button, a project or the scrim moved it: animate there.
		m.drawerOffset = ui.Point{X: want}
	}
	ui.Box(c.Key("drawer-clip")).Fill().Clip().Children(func() {
		row := ui.Row(c.Key("drawer-row")).Width(w+screen).Shrink(0).FillHeight().Margin(0, 0, 0, -w).AlignItems(ui.Stretch).Background(p.drawer)
		row.Draggable(&m.drawerOffset, ui.DraggableOptions{
			Axis:      ui.Horizontal,
			Bounds:    &ui.Rect{W: w},
			Snap:      ui.Nearest(ui.Point{}, ui.Point{X: w}),
			Animation: drawerMotion,
		})
		shown := max(0, min(1, row.DragOffset().X/w))
		if _, _, ended := row.DragEnded(); ended {
			m.menu = m.drawerOffset.X > w/2
		}
		m.drawerDragging = row.Dragging()
		m.drawerVisible = shown > 0.001
		quotaShown := m.drawerVisible || m.menu || m.drawerDragging
		if quotaShown && !m.quotaDrawerShown && m.connected && m.conn != nil {
			m.sendPhoneAction(remote.Request{Op: "quota"})
		}
		if !quotaShown {
			m.quotaOpen = false
		}
		m.quotaDrawerShown = quotaShown
		row.Children(func() {
			ui.Column(c.Key("drawer")).Width(w).FillHeight().Background(p.drawer).Padding(safe.Top+12, 24, safe.Bottom+16, safe.Left+24).Gap(12).Children(func() {
				if m.drawerVisible || m.menu || m.drawerDragging {
					m.drawerContent(c)
				}
			})
			background := m.homeBackground()
			if path != "/" {
				background = p.bg
			}
			if m.dark {
				background = background.Mix(ui.Hex("#141414"), shown)
			}
			// Keep the same clip while moving, and restore the resting page
			// continuously instead of changing its surface on the final frame.
			main := ui.Column(c.Key("main")).Width(screen).FillHeight().Background(background).
				Padding(safe.Top, safe.Right, safe.Bottom, safe.Left).Radius(32*shown, 0, 0, 32*shown).Clip()
			main.Children(func() {
				switch path {
				case "/thread":
					m.threadView(c)
				case "/memory":
					m.memoryPage(c)
				case "/automations":
					m.automationsPage(c, false)
				case "/settings":
					m.settingsView(c)
				case "/desktops":
					m.desktopsView(c)
				default:
					m.listView(c)
				}
				if m.drawerVisible {
					// The scrim covers the list: a tap on it closes the drawer,
					// a drag on it moves the drawer.
					ui.Box(c.Key("scrim")).Label("关闭侧边栏").Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, 0.45*shown))
				} else {
					// Reserve the left edge for pulling out the drawer.
					ui.Box(c.Key("edge")).Absolute().Top(0).Left(0).Bottom(0).Width(24)
				}
			})
		})
		// A finger moves a few points as it taps, which MyGo counts as a drag
		// and no click; letting go of a press that began on the scrim closes
		// the drawer either way.
		// Only while the drawer shows: asking for clicks makes the row a
		// click target, which would keep the edge from pulling it out.
		// A finger leaves no pointer over the scrim once it lifts, so where
		// the press began decides, and only a press on an open drawer: one
		// pulling it out from the edge ends right of it too.
		if m.drawerVisible {
			pressed := row.Pressed()
			clicked := row.Clicked()
			if pressed && !m.rowPressed {
				m.pressX, m.pressOpen = -1, m.menu
				if x, _, ok := row.PointerPosition(); ok {
					m.pressX = x
				}
			}
			released := m.rowPressed && !pressed
			m.rowPressed = pressed
			if m.pressOpen && m.pressX >= w && (clicked || released) {
				m.menu, m.pressOpen = false, false
			}
		} else {
			m.rowPressed, m.pressOpen = false, false
		}
	})
	// Buttons and scrim taps commit during this frame; drags commit on
	// release. Give feedback now, without waiting for the slide to finish.
	if !m.drawerDragging && m.drawerFeedbackOpen != m.menu {
		m.drawerFeedbackOpen = m.menu
		mobileFeedback(mygo.HapticSoft)
	}
}

// drawerContent is the drawer: the desktop, its agent quotas, the
// projects to show, and the connection's actions.
func (m *mobileApp) drawerContent(c *ui.Context) {
	p := m.pal
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		m.drawerDesktopSwitcher(c)
		label := "搜索对话"
		if m.drawerSearch {
			label = "关闭搜索"
		}
		if m.circle(c, "search", label, func() { icon(c, "search", 22, p.ink) }).Clicked() {
			m.drawerSearch = !m.drawerSearch
			m.drawerQuery = ""
		}
	})
	if m.drawerSearch {
		ui.TextInput(c.Key("drawer-query"), &m.drawerQuery).FillWidth().MinHeight(44).Padding(10, 12).Radius(12).
			Background(p.surface).Border(0, p.surface).FontSize(m.font(16)).Label("搜索对话").Placeholder("搜索对话标题").AutoFocus()
	}
	if m.err != "" {
		ui.Text(c, m.err).FontSize(m.font(13)).TextColor(danger).LineHeight(1.4)
	}
	m.drawerQuotas(c.Key("drawer-quotas"))

	ui.Scroll(c.Key("drawer-projects")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			if m.drawerSearch {
				matches := matchingThreadRefs(m.state.Projects, 0, strings.TrimSpace(m.drawerQuery))
				if len(matches) == 0 {
					ui.Text(c, "没有匹配的对话").Padding(12, 0).FontSize(m.font(14)).TextColor(p.muted)
				}
				for _, match := range matches {
					m.drawerThread(c.Key(match.ID), match.Thread)
				}
				return
			}
			if m.phoneButton(c.Key("memory")).FillWidth().MinHeight(48).Padding(0).Gap(12).Label("共享记忆").Children(func() { icon(c, "stack-2", 21, p.ink); ui.Text(c, "共享记忆").FontSize(m.font(17)) }).Clicked() {
				m.memories.requestedAt = time.Time{}
				m.openDrawerPage("/memory")
			}
			automations := m.phoneButton(c.Key("automations")).FillWidth().MinHeight(48).Padding(0).Gap(12).Radius(12).Label("自动化")
			automations.Children(func() {
				icon(c, "clock", 21, p.ink)
				ui.Text(c, "自动化").FontSize(m.font(17)).FontWeight(400)
			})
			if automations.Clicked() {
				m.autos.requestedAt = time.Time{}
				m.openDrawerPage("/automations")
			}
			ui.Box(c.Key("navigation-separator")).FillWidth().Height(1).Margin(16, 0, 12, 0).Background(p.line)
			ui.Text(c, "项目").FontSize(m.font(13)).TextColor(p.muted).Margin(0, 0, 6, 0)
			if !m.projectsExpanded && len(m.state.Projects) > 0 {
				m.expandedProject, m.projectsExpanded = m.selectedProject().Path, true
			}
			for _, pr := range m.state.Projects {
				m.drawerProject(c.Key("drawer:"+pr.Path), pr)
			}
		})
	})
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		newThread := m.phoneButton(c.Key("new-thread")).MinWidth(0).Shrink(1).MinHeight(48).Padding(0, 20).Gap(10).Radius(24).Background(p.ink).TextColor(p.bg).Label("新建对话")
		newThread.Children(func() {
			icon(c, "edit", 21, p.bg)
			ui.Text(c, "新建对话").FontSize(m.font(16)).FontWeight(600).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…")
		})
		if newThread.Clicked() {
			m.startConversation(m.selectedProject().Path, m.composerAgent())
		}
		ui.Spacer(c)
		if m.circle(c, "settings", "设置", func() { icon(c, "settings", 22, p.ink) }).Size(48, 48).Radius(24).Clicked() {
			m.openDrawerPage("/settings")
		}
	})
}

// drawerMotion settles the drawer as iOS does: a spring that does not
// bounce, keeping the speed of a throw.
var drawerMotion = ui.Animation{Spring: ui.SpringOptions{Duration: 380 * time.Millisecond}}

// showProject selects the phone's workspace and toggles its conversation list.
func (m *mobileApp) showProject(path string) {
	m.selected, m.projectsExpanded = path, true
	if m.expandedProject == path {
		m.expandedProject = ""
	} else {
		m.expandedProject = path
	}
}

// threadStatus marks conversations waiting for approval, running, or unread.
func (m *mobileApp) threadStatus(c *ui.Context, t Thread) {
	p := m.pal
	switch {
	case len(m.approvalsFor(t.ServerID)) > 0:
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Box(c.Key("dot")).Size(7, 7).Radius(4).Background(p.warn)
			ui.Text(c, "待审批").FontSize(m.font(12)).TextColor(p.warn)
		})
	case t.Busy:
		ui.Spinner(c).FontSize(m.font(13))
	default:
		if _, unread := m.unreadFor(t); unread {
			ui.Box(c).Size(8, 8).Radius(4).Background(p.ok)
		}
	}
}

func (m *mobileApp) threadView(c *ui.Context) {
	p := m.pal
	cur := m.view.c.current()
	listed, proj := m.find(m.open)
	title, agent, busy := m.title, cur.Agent, cur.Busy || listed.Busy
	if listed.Title != "" {
		title = listed.Title
	}
	if agent == "" {
		agent = listed.Agent
	}
	if m.open == 0 {
		title, agent, busy, proj = "新对话", m.newAgent, false, m.project(m.newProject)
	}
	if agent == "" {
		agent = agentCodex
	}
	if title == "" {
		title = "对话"
	}
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/thread")
		ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(2).Children(func() {
			ui.Text(c, nativeText(title)).FontSize(m.font(16)).FontWeight(600).SingleLine().Ellipsis("…")
			sub := agentName(agent)
			if proj.Name != "" {
				sub += " · " + proj.Name
			}
			if line := m.connectionLine(); line != "" {
				sub = line
			}
			ui.Text(c, sub).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
		})
		more := m.circle(c, "more", "更多", func() { icon(c, "dots", 20, p.ink) })
		m.conversationActions(c, more, proj.Path, agent)
	})
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		if m.open == 0 {
			m.newConversationView(c, proj)
		} else {
			m.view.messageList(c, cur)
		}
	})
	for _, a := range m.approvalsFor(cur.ServerID) {
		ui.Column(c.Key("approval:" + string(a.ID))).FillWidth().Children(func() { m.approvalCard(c, a) })
	}
	m.composer(c, agent, busy)
}

func (m *mobileApp) newConversationView(c *ui.Context, proj phoneProject) {
	p := m.pal
	agent := m.composerAgent()
	ui.Scroll(c.Key("draft-intro")).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().MinHeight(220).AlignItems(ui.Center).Justify(ui.Center).Gap(14).Padding(24).Children(func() {
			agentIcon(c, agent, 46)
			ui.Text(c, "Let’s build").FontSize(m.font(28)).FontWeight(600)
			pick := m.phoneButton(c.Key("pick-project")).MinHeight(44).Gap(8).Label("选择项目")
			pick.Children(func() {
				ui.Text(c, proj.Name).FontSize(m.font(24)).TextColor(p.muted).SingleLine().Ellipsis("…")
				icon(c, "chevron-down", 16, p.muted)
			})
			pick.PopoverMenuButton(func(menu *ui.Menu) {
				for _, project := range m.state.Projects {
					if menu.Item(project.Name).Radio(project.Path == proj.Path).Chosen() {
						m.selected = project.Path
						m.expandedProject, m.projectsExpanded = project.Path, true
						if m.newProject != "" {
							m.newProject = project.Path
						}
					}
				}
			})
			ui.Row(c).FillWidth().Wrap().Justify(ui.Center).Gap(8).Children(func() {
				for _, a := range []string{agentCodex, agentClaude} {
					if m.pill(c, "agent:"+a, agentName(a), agent == a).Clicked() {
						m.newAgent = a
					}
				}
			})
		})
	})
}

func (m *mobileApp) composerAgent() string {
	if m.newAgent != "" {
		return m.newAgent
	}
	if m.state.Agent != "" {
		return m.state.Agent
	}
	return agentCodex
}

// composer is the floating message box: the agent, the text, the model's
// effort, and a button that sends, or stops a running turn.
func (m *mobileApp) composer(c *ui.Context, agent string, busy bool) {
	p := m.pal
	m.inlineInput.commands, _ = m.composerCommands(agent, m.composerProject())
	owner := fmt.Sprintf("%s:%d:%s", m.composerProject(), m.open, agent)
	if m.inlineInput.doc != nil && m.inlineInput.owner == owner {
		m.inlineInput.sync(m.draft, func(text string) { m.draft = text })
	}
	ui.Column(c).FillWidth().Padding(6, 12, 10, 12).Gap(6).Children(func() {
		if m.sendErr != "" {
			ui.Text(c, m.sendErr).FontSize(m.font(13)).TextColor(danger).Margin(0, 8)
		}
		m.commandMenu(c, agent, m.composerProject())
		m.threadMenu(c, m.composerProject())
		m.queueView(c)
		m.taskStatus(c)
		m.thumbnails(c)
		if hint := m.inlineInput.commandHint(); hint != "" {
			ui.Text(c, hint).FontSize(m.font(12)).TextColor(p.muted).FillWidth().Wrap().Padding(0, 10)
		}
		ui.Column(c.Key("composer-box")).FillWidth().Padding(6, 6, 6, 10).Background(p.surface).Radius(26).Children(func() {
			opts := m.sendOptions(agent)
			effort := strings.ToUpper(firstNonEmpty(opts.Effort, "默认"))
			hasDraft := strings.TrimSpace(m.draft) != "" || len(m.images) > 0
			steerAvailable := busy && hasDraft
			showEffort := effort != "" && c.Root().Bounds().W >= 360 && m.textScale <= 1.3 && !steerAvailable
			right := float32(52)
			if steerAvailable {
				right += 72
			}
			if showEffort {
				w, _ := c.MeasureText(0, ui.Span{Text: effort, Size: m.font(12)})
				right += max(44, w) + 8
			}
			safe := c.SafeArea()
			width := c.Root().Bounds().W - safe.Left - safe.Right - 24 - 16 - 90 - right
			style := mentionStyle(c, m.font(16))
			m.inlineInput.style, m.inlineInput.title = style, m.view.referenceTitle
			text := m.inlineInput.text(m.draft)
			_, h := ui.ShapeRichText(inlineThreadSpans(text, m.font(16)), style.font, max(1, width)).Size()
			_, line := c.MeasureText(0, ui.Span{Text: "M", Size: m.font(16)})
			expanded := strings.Contains(m.draft, "\n") || h > line*1.5 || steerAvailable
			if text.Len() > 0 {
				if a := text.AttributesAt(0).Attachment; a != nil && a.Width > width {
					expanded = true
				}
			}
			area := ui.Column(c.Key("input-area")).FillWidth().MinHeight(44).Justify(ui.Center).Padding(0, right, 0, 90)
			if expanded {
				area.MinHeight(0).Justify(ui.Start).Padding(4, 8, 54, 4)
			}
			area.Children(func() {
				placeholder := agentName(agent)
				if opts.Model != "" {
					placeholder = opts.Model
				}
				inputWidth := width
				if expanded {
					inputWidth = c.Root().Bounds().W - safe.Left - safe.Right - 24 - 28
				}
				// Clip in a parent: EditorBase's custom drawing can extend beyond
				// its own bounds when it scrolls a long paragraph to the caret.
				ui.Column(c.Key("editor-clip")).FillWidth().Clip().Children(func() {
					input := m.view.threadInput(c.Key("composer"), &m.inlineInput, m.draft, owner, "消息", placeholder, m.font(16), inputWidth, 1, 6, func(text string) { m.draft = text })
					// EditorBase padding does not inset its text. Center it
					// using the enclosing column instead.
					input.MinHeight(0).Padding(0)
				})
			})
			ui.Row(c.Key("left-tools")).Absolute().Left(4).Bottom(6).Width(88).MinHeight(44).AlignItems(ui.Center).Children(func() {
				// Bring the icons closer within separate 44-point hit areas.
				add := m.phoneButton(c.Key("attach")).Size(44, 44).Padding(0, 6, 0, 0).Justify(ui.End).Label("添加图片")
				add.Children(func() { icon(c, "plus", 20, p.muted) })
				if add.Clicked() {
					m.addPhotos()
				}
				agentButton := m.phoneButton(c.Key("agent")).Size(44, 44).Padding(0, 0, 0, 6).Justify(ui.Start).Label("模型设置")
				agentButton.Children(func() { agentIcon(c, agent, 20) })
				if agentButton.Clicked() {
					m.openOptions(agent)
				}
			})
			ui.Row(c.Key("right-tools")).Absolute().Right(6).Bottom(6).Width(right - 8).MinHeight(44).Gap(8).AlignItems(ui.Center).Children(func() {
				if steerAvailable {
					b := m.phoneButton(c.Key("steer")).Size(64, 44).Padding(0).Justify(ui.Center).Label("立即引导").Disabled(m.sending)
					b.Children(func() { ui.Text(c, "引导").FontSize(m.font(12)).TextColor(p.ink) })
					if b.Clicked() {
						m.sendDraft(true)
					}
				}
				if showEffort {
					b := m.phoneButton(c.Key("effort")).MinHeight(44).Shrink(0).Padding(0).Label("推理强度")
					b.Children(func() { ui.Text(c, effort).FontSize(m.font(12)).TextColor(p.muted) })
					if b.Clicked() {
						m.openOptions(agent)
					}
				}
				canSend := m.connected && !m.sending && (strings.TrimSpace(m.draft) != "" || len(m.images) > 0)
				bg, fg, name, label := p.line, p.muted, "arrow-up", "发送"
				if busy && !hasDraft {
					bg, fg, name, label = p.ink, p.bg, "x", "停止"
				} else if busy {
					bg, fg, label = p.ink, p.bg, "加入队列"
				} else if canSend {
					bg, fg = p.ink, p.bg
				}
				b := m.phoneButton(c.Key("send")).Size(44, 44).Shrink(0).Padding(0).Radius(22).Background(bg).Justify(ui.Center).Label(label)
				b.Children(func() {
					if m.sending {
						ui.Spinner(c).FontSize(m.font(12)).TextColor(fg)
					} else {
						icon(c, name, 18, fg)
					}
				})
				if b.Clicked() {
					if busy && !hasDraft {
						m.stop()
					} else if canSend {
						m.send()
					}
				}
			})
		})
	})
}

// addPhotos picks photos from the library with the system picker, which
// needs no access to the whole library.
func (m *mobileApp) addPhotos() {
	if len(m.images) >= maxPhoneImages {
		m.sendErr = fmt.Sprintf("一次最多发送 %d 张图片", maxPhoneImages)
		return
	}
	win := m.win
	go func() {
		paths, err := mygo.Dialog.Photos(mygo.PhotoDialogOptions{Parent: win, Multiple: true})
		runOnMain(func() {
			switch {
			case errors.Is(err, mygo.ErrUnsupported):
				m.sendErr = "这台设备不支持选择图片"
			case err != nil:
				m.sendErr = "无法打开相册：" + err.Error()
			}
			for _, p := range paths {
				if len(m.images) >= maxPhoneImages {
					m.sendErr = fmt.Sprintf("一次最多发送 %d 张图片", maxPhoneImages)
					go os.Remove(p)
					continue
				}
				m.images = append(m.images, p)
			}
		})
	}()
}

func (m *mobileApp) removeImage(i int) {
	if i < 0 || i >= len(m.images) {
		return
	}
	p := m.images[i]
	m.images = append(m.images[:i:i], m.images[i+1:]...)
	go os.Remove(p)
}

// clearImages drops the attached photos and their cache copies.
func (m *mobileApp) clearImages() {
	for _, p := range m.images {
		go os.Remove(p)
	}
	m.images = nil
}

// thumbnails shows the photos to send above the composer.
func (m *mobileApp) thumbnails(c *ui.Context) {
	if len(m.images) == 0 {
		return
	}
	p := m.pal
	ui.Row(c.Key("thumbs")).Gap(8).Padding(0, 4).Children(func() {
		for i, path := range m.images {
			ui.Row(c.Key("thumb:"+path)).Size(64, 64).Radius(12).Background(p.surface).Clip().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				if img := cachedImage(path); img != nil {
					ui.Image(c.Key("img"), img).Size(64, 64).Fit(ui.Cover)
				} else {
					icon(c, "file", 22, p.muted)
				}
				rm := m.phoneButton(c.Key("rm")).Absolute().Top(0).Right(0).Size(44, 44).Padding(0).Justify(ui.Center).Label("移除图片")
				rm.Children(func() {
					ui.Box(c).Size(22, 22).Radius(11).Background(ui.RGBA(0, 0, 0, 0.6)).AlignItems(ui.Center).Justify(ui.Center).Children(func() { icon(c, "x", 12, ui.Hex("#ffffff")) })
				})
				if rm.Clicked() {
					m.removeImage(i)
				}
			})
		}
	})
}

// composerProject is the project of the conversation being written in.
func (m *mobileApp) composerProject() string {
	if m.open != 0 {
		if _, p := m.find(m.open); p.Path != "" {
			return p.Path
		}
	}
	if m.newProject != "" {
		return m.newProject
	}
	return m.selectedProject().Path
}

// requestCommands asks the desktop for a project's Claude Code commands,
// once per connection.
func (m *mobileApp) requestCommands(project string) {
	conn := m.conn
	if project == "" || conn == nil || m.asked[project] {
		return
	}
	if m.asked == nil {
		m.asked = map[string]bool{}
	}
	m.asked[project] = true
	go conn.Send(remote.Request{Op: "commands", Project: project})
}

func (m *mobileApp) composerCommands(agent, project string) ([]composerChoice, bool) {
	if agent == agentClaude {
		cmds, known := m.commands[project]
		return cmds, known
	}
	goal := composerCommands[0]
	goal.Detail = "设置目标 · /goal pause · /goal resume · /goal clear"
	return []composerChoice{goal}, true
}

// commandMenu lists commands while the draft is a slash and a command's beginning.
func (m *mobileApp) commandMenu(c *ui.Context, agent, project string) {
	d := m.draft
	if !strings.HasPrefix(d, "/") || strings.ContainsAny(d, " \n") {
		return
	}
	p := m.pal
	q := strings.ToLower(d[1:])
	if agent == agentClaude {
		m.requestCommands(project)
	}
	cmds, known := m.composerCommands(agent, project)
	var shown []composerChoice
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.Name), q) {
			shown = append(shown, cmd)
		}
	}
	ui.Column(c.Key("commands")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
		switch {
		case !known:
			ui.Row(c).Gap(8).Padding(10).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).FontSize(m.font(12))
				ui.Text(c, "正在读取 Claude Code 命令…").FontSize(m.font(13)).TextColor(p.muted)
			})
		case len(shown) == 0:
			ui.Text(c, "没有匹配的命令").FontSize(m.font(13)).TextColor(p.muted).Padding(10)
		default:
			ui.Scroll(c.Key("command-list")).FillWidth().MaxHeight(260).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for _, cmd := range shown {
						item := m.phoneButton(c.Key("cmd:"+cmd.Name)).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label("/" + cmd.Name)
						item.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
								ui.Text(c, "/"+cmd.Name).FontSize(m.font(15)).FontWeight(500).SingleLine().Ellipsis("…")
								if cmd.Detail != "" {
									ui.Text(c, cmd.Detail).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
								}
							})
						})
						if item.Clicked() {
							m.draft = "/" + cmd.Name + " "
						}
					}
				})
			})
		}
	})
}

// phoneCachedThreads bounds how many conversations the phone keeps.
const phoneCachedThreads = 30

func (m *mobileApp) cachedMessages(serverID string) []Message {
	if serverID == "" {
		return nil
	}
	if msgs, ok := m.msgCache[serverID]; ok {
		return msgs
	}
	msgs := m.loadDisk(serverID)
	if len(msgs) > 0 {
		m.keep(serverID, msgs)
	}
	return msgs
}

// remember keeps a conversation's messages, dropping the oldest kept.
func (m *mobileApp) remember(serverID string, msgs []Message) {
	m.keep(serverID, msgs)
	m.persist(serverID, msgs)
}

// keep holds a conversation's messages in memory, dropping the oldest.
func (m *mobileApp) keep(serverID string, msgs []Message) {
	if serverID == "" {
		return
	}
	if m.msgCache == nil {
		m.msgCache = map[string][]Message{}
	}
	for i, k := range m.msgKeys {
		if k == serverID {
			m.msgKeys = append(m.msgKeys[:i:i], m.msgKeys[i+1:]...)
			break
		}
	}
	m.msgKeys = append(m.msgKeys, serverID)
	m.msgCache[serverID] = msgs
	for len(m.msgKeys) > phoneCachedThreads {
		delete(m.msgCache, m.msgKeys[0])
		m.msgKeys = m.msgKeys[1:]
	}
}

// subscribeRequest subscribes to a conversation, saying what the phone
// holds of it so the desktop sends only the rest.
func (m *mobileApp) subscribeRequest(id int) remote.Request {
	r := m.threadRequest("subscribe", id)
	if id == 0 {
		return r
	}
	for _, msg := range m.cachedMessages(r.ServerID) {
		r.Have = append(r.Have, remote.Seen{ID: msg.ID, FP: messageFingerprint(msg)})
	}
	return r
}

func (m *mobileApp) threadRequest(op string, id int) remote.Request {
	r := remote.Request{Op: op, Thread: id}
	if id == 0 {
		return r
	}
	t, p := m.find(id)
	// The currently displayed page is authoritative when a new state has
	// already reused its numeric ID for a different conversation.
	if cur := m.view.c.current(); cur.ID == id && cur.ServerID != "" {
		if cur.ServerID != t.ServerID || threadAgent(cur) != threadAgent(t) {
			p = phoneProject{}
		}
		t = cur
	}
	r.ServerID, r.Agent, r.Project = t.ServerID, threadAgent(t), p.Path
	return r
}

// applyThread merges a delta into what the phone holds of the conversation.
func (m *mobileApp) applyThread(d phoneThreadDelta) {
	t := d.Thread
	var base []Message
	if !d.Reset {
		if cur := m.view.c.current(); cur.ID == t.ID && cur.ServerID == t.ServerID {
			base = cur.Messages
		}
		if len(base) == 0 {
			base = m.cachedMessages(t.ServerID)
		}
		// Busy, loading, usage and title updates carry no new messages.
		// Reuse the history and its presentation groups instead of copying,
		// regrouping and persisting the entire conversation on each update.
		if len(d.Changed) == 0 && sameMessageOrder(base, d.Order) {
			t.Messages = base
			m.updateThreadView(t, false)
			return
		}
	}
	known := make(map[int]Message, len(base)+len(d.Changed))
	for _, x := range base {
		known[x.ID] = x
	}
	for _, x := range d.Changed {
		known[x.ID] = x
	}
	list := make([]Message, 0, len(d.Order))
	for _, id := range d.Order {
		if x, ok := known[id]; ok {
			list = append(list, x)
		}
	}
	t.Messages = list
	m.remember(t.ServerID, list)
	m.showThread(t)
}

func sameMessageOrder(messages []Message, order []int) bool {
	if len(messages) != len(order) {
		return false
	}
	for i, m := range messages {
		if m.ID != order[i] {
			return false
		}
	}
	return true
}
