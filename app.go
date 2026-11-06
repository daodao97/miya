package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// chatApp holds what only the interface needs; conversations, connections
// and settings stay in the controller.
type chatApp struct {
	c *chatController
	*conversationView

	sidebar   bool
	section   string // "", "Settings", "Automations" or "Skills"
	menu      string // the composer's or header's open menu
	modelOpen bool
	effortOpen,
	agentOpen,
	permissionsOpen bool
	notice         string
	focusComposer  bool
	shortcutsOpen  bool
	switcher       conversationSwitcher
	commandPalette desktopCommandPalette

	workspace, projectName, projectPath string
	expandedProject                     string
	projectDialog                       bool
	projectError                        string
	projectBrowse                       bool
	projectBrowseLoading                bool
	projectDirectories                  []hostEntry
	threadListExpanded                  map[string]bool
	desktopReadIntent                   desktopRead // a completed result the user chose to view

	answers        map[string]map[string]string // question answers, by approval ID
	forms          map[string]string            // MCP form JSON, by approval ID
	formErrors     map[string]string
	settingsTab    int
	settingDrafts  map[string]string
	serviceRestart serviceRestartUI
	workspaceHub   *desktopWorkspaces

	// appearance is "", "light" or "dark"; dark and themed the palette applied.
	appearance   string
	dark, themed bool

	// editing is the user message being edited, 0 for none.
	editing   int
	editText  string
	editInput inlineThreadInput

	complete           completion
	memories           memoryUI
	memoryNotes        map[string]*memoryNoticeUI
	memoryRevisionSeen uint64
	autos              automationsUI
	collaborations     collaborationUI
	account            accountView
	usage              *desktopUsage
	openWindow         func()
	quitting           bool

	panels       map[int]*panelState
	panelVisible bool
	panelWidth   float32
	nextSurface  int
}

func newChatApp() *chatApp {
	c := newWindowController()
	app := newChatAppWithController(c)
	app.ensureDesktopWorkspaces()
	return app
}

func newChatAppWithController(c *chatController) *chatApp {
	app := &chatApp{
		c:                  c,
		conversationView:   newConversationView(c),
		sidebar:            true,
		workspace:          c.cwd,
		projectName:        filepath.Base(c.cwd),
		projectPath:        c.cwd,
		expandedProject:    c.cwd,
		threadListExpanded: map[string]bool{},
		answers:            map[string]map[string]string{},
		forms:              map[string]string{},
		formErrors:         map[string]string{},
		settingDrafts:      map[string]string{},
		appearance:         normalizeAppearance(loadSetting("appearance")),
	}
	app.resolveTitle = app.desktopReferenceTitle
	app.onRetry = func(req retryRequest) {
		if err := c.retryResponse(req); err != nil {
			app.notice = err.Error()
		}
	}
	app.onHandoff = app.handoffToNewThread
	app.desktopAccessory = app.collaborationToggle
	app.userGroup = app.userGroupView
	app.memoryNotice = app.memoryNoticeView
	app.pasteAttachments = app.pasteAttachmentsData
	app.setNotice = func(s string) { app.notice = s }
	app.needsReadFrame = func(t Thread, state *ui.ListState, groups int) bool {
		return !app.projectDialog && !app.switcher.open && !app.shortcutsOpen && !app.commandPalette.open && !t.Busy && !t.Loading && t.HistoryLoaded && groups > 0 && c.desktopThreadUnread(t) && state.AtEnd()
	}
	app.md = markdown.Options{
		OnLink:    app.openMessageLink,
		LoadImage: func(src string) (ui.ImageSource, error) { return loadImage(imageSourcePath(src)) },
	}
	if c.remoteHost() {
		app.imagePreview = app.remoteMessageImage
		app.md.LoadImage = app.loadRemoteImage
	}
	app.account.init(app)
	app.panelWidth = 460
	c.commandsChanged = app.refreshCommandChoices
	onCleanup(app.closePanels)
	return app
}

// imageSourcePath maps a Markdown image to something loadImage reads.
func imageSourcePath(src string) string {
	if strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "file://") {
		return src
	}
	return nativeText(src)
}

func main() {
	if runtime.GOOS == "ios" {
		mobileEntry()
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "-desktop-privacy" {
		if err := desktop.RunPrivacyHelper(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Agents Miya runs start it as their MCP server for the conversations.
	if len(os.Args) > 1 && os.Args[1] == "-mcp" {
		if err := runMCP(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-server" {
		if err := runService(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-serve" {
		if err := serveCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "-connection" {
		data, err := os.ReadFile(connectionFile())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(data))
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "-restart-when-idle" {
		if err := requestServerRestart(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-connect" {
		if len(os.Args) != 3 {
			log.Fatal("usage: miya -connect <miya://connect link>")
		}
		if err := saveRemoteServer(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		os.Args = os.Args[:1]
	}
	app := newChatApp()
	configureInterfaceLanguage()
	mygo.App.OnLocaleChanged(func(tag string) {
		locales.SetCurrent(tag)
		app.activeWorkspaceApp().syncUsageTray()
	})
	mygo.Theme.SetSource(themeSource(app.appearance))
	// With the background service running, the service serves phones: a
	// window only mirrors the current project.
	if app.c.remote == nil {
		desktopPhone = newPhoneHost(app.c)
	}
	open := func() {
		if w := currentWindow(); w != nil && !w.IsDestroyed() {
			w.Show()
			w.Focus()
			return
		}
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:                "Codex Chat",
			Width:                1180,
			Height:               820,
			MinWidth:             780,
			MinHeight:            580,
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 18, Y: 20},
			BackgroundColor:      windowBackground(app.activeWorkspaceApp()),
			StateKey:             "main",
			Content:              ui.View(app.view),
		})
		setAppWindow(w)
		w.OnFocus(func() {
			w.Update(func() {
				active := app.activeWorkspaceApp()
				if active.section == "" {
					active.requestDesktopRead(active.c.current())
					active.markVisibleThreadRead()
				}
			})
		})
		w.OnClose(func(e *mygo.CloseEvent) {
			active := app.activeWorkspaceApp()
			if !app.quitting && runtime.GOOS == "darwin" && active.usage != nil && active.usage.tray != nil {
				e.PreventDefault()
				w.Hide()
			}
		})
		w.OnClosed(func() {
			if currentWindow() == w {
				setAppWindow(nil)
			}
		})
	}
	app.openWindow = open
	mygo.App.WhenReady(func() {
		watchSystemAppearance()
		open()
		app.startUsage()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		active := app.activeWorkspaceApp()
		if !app.quitting && !hasVisibleWindows && (active.usage == nil || active.usage.panel == nil) {
			open()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { app.quitting = true; app.activeWorkspaceApp().stopUsage() })
	mygo.App.OnQuit(runCleanups)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (app *chatApp) project() string { return app.projectName }

func (app *chatApp) closeMenus() {
	app.menu = ""
	app.modelOpen, app.effortOpen, app.agentOpen, app.permissionsOpen = false, false, false, false
}

func (app *chatApp) newThread() {
	app.c.newThread()
	app.section = ""
	app.closeMenus()
	app.focusComposer = true
}

func (app *chatApp) selectThread(t Thread) {
	if path := app.c.threadWorkspace(t.ID); path != app.c.cwd {
		if err := app.c.changeWorkspace(path); err != nil {
			app.c.setStatus("Project selection failed: " + err.Error())
			return
		}
		app.workspace, app.projectPath, app.expandedProject = path, path, path
		for _, p := range app.c.projects() {
			if p.Path == path {
				app.projectName = p.Name
				break
			}
		}
	}
	app.c.selectThread(t)
	app.requestDesktopRead(t)
	app.section = ""
	app.closeMenus()
	app.focusComposer = true
}

// send runs a slash command or sends the draft.
func (app *chatApp) send() { app.sendDraft(false) }

// sendDraft runs a slash command or sends the draft; while a turn runs, it
// steers it, or queues the draft when steer is false.
func (app *chatApp) sendDraft(steer bool) {
	c := app.c
	// In a Claude Code conversation, the commands Miya does not run itself
	// (skills, custom commands, /compact…) go to Claude Code as messages.
	if command, ok := slashCommand(c.draft()); ok && !claudeTakesCommand(c.currentAgent(), command) {
		app.notice = ""
		clear := func() { c.rememberDraft(""); app.menu = "" }
		if _, goal, _ := parseGoalCommand("/" + command); goal {
			app.notice = c.runThreadCommand(command)
			return
		}
		switch command {
		case "new":
			app.newThread()
		case "model":
			clear()
			app.effortOpen = false
			app.modelOpen = true
		case "permissions":
			clear()
			app.section = "Settings"
		case "mention":
			c.rememberDraft("@")
			app.complete.edit(app, "@")
		case "diff":
			clear()
			app.openSurface(app.panel(), surfaceDiff)
		case "status":
			clear()
			app.notice = fmt.Sprintf("Model: %s · Thinking: %s · Permissions: %s · Tokens: %d", c.model(), c.effort(), permissionChoiceTitle(c.currentPermissionPolicy()), c.current().Tokens)
		case "fast", "fast on", "fast off":
			clear()
			app.notice = c.runThreadCommand(command)
		case "compact", "review":
			app.notice = c.runThreadCommand(command)
		default:
			app.notice = "Unknown command. Type / to choose a supported command."
		}
		return
	}
	app.notice = ""
	before := c.active()
	if id := c.sendDraft(steer); id != 0 {
		app.listFor(id).ScrollToEnd()
	}
	if c.lastSubmitError != "" {
		app.notice = c.lastSubmitError
	}
	if before == 0 && c.active() != 0 {
		app.adoptDraftPanel(c.active())
	}
	app.section = ""
	app.menu = ""
}

func (app *chatApp) attachFiles() {
	cwd := app.c.cwd
	if app.c.remoteHost() {
		cwd, _ = os.UserHomeDir()
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      currentWindow(),
			Title:       tr("Add files"),
			DefaultPath: cwd,
			ButtonLabel: tr("Attach"),
			Multiple:    true,
		})
		if err == nil && len(paths) > 0 {
			app.c.dispatch(func() { app.c.addAttachments(paths) })
		}
	}()
}

// pasteAttachments turns files or an image on the clipboard into
// attachments. It reports whether it found any, so that text pastes as text.
func (app *chatApp) pasteAttachmentsData(clipboard transfer.Data) bool {
	if paths, err := clipboard.Files(); err == nil && len(paths) > 0 {
		app.c.addAttachments(paths)
		return true
	}
	data, err := clipboard.Read(transfer.PNG)
	if err != nil || len(data) == 0 {
		return false
	}
	file, err := os.CreateTemp("", "codex-clipboard-*.png")
	if err != nil {
		return false
	}
	name := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return false
	}
	_ = file.Close()
	app.c.addAttachments([]string{name})
	return true
}

func (app *chatApp) openProjectDialog() {
	app.projectPath = app.workspace
	app.projectError = ""
	app.projectDialog = true
	app.projectBrowse, app.projectDirectories = false, nil
	app.closeMenus()
}

func (app *chatApp) openWorkspace() {
	if app.c.remoteHost() {
		app.openSurface(app.panel(), surfaceFiles)
		return
	}
	path := app.workspace
	go func() {
		for _, name := range []string{"code", "zed"} {
			bin, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			if err := exec.Command(bin, path).Start(); err == nil {
				return
			}
		}
		if err := mygo.Shell.OpenPath(path); err != nil {
			app.c.dispatch(func() { app.c.setStatus("Unable to open workspace: " + err.Error()) })
		}
	}()
}

func (app *chatApp) browseProject() {
	if app.c.remoteHost() {
		app.browseServerProject(app.projectPath)
		return
	}
	start := app.workspace
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            currentWindow(),
			Title:             tr("Select project directory"),
			DefaultPath:       start,
			ButtonLabel:       tr("Choose"),
			Directory:         true,
			CreateDirectories: true,
		})
		app.c.dispatch(func() {
			if err != nil {
				app.projectError = "Folder selection failed: " + err.Error()
				return
			}
			if len(paths) > 0 {
				app.projectPath = paths[0]
				if strings.TrimSpace(app.projectName) == "" {
					app.projectName = filepath.Base(paths[0])
				}
			}
		})
	}()
}

func (app *chatApp) confirmProject() {
	c := app.c
	path := strings.TrimSpace(app.projectPath)
	name := strings.TrimSpace(app.projectName)
	if name == "" {
		app.projectError = "Enter a project name."
		return
	}
	if err := c.changeWorkspace(path); err != nil {
		app.projectError = err.Error()
		return
	}
	app.workspace = c.cwd
	app.projectName = name
	app.expandedProject = c.cwd
	c.renameProject(c.cwd, name)
	app.projectDialog = false
	app.projectError = ""
	app.section = ""
}

func (app *chatApp) selectProject(p Project) {
	c := app.c
	if p.Path == app.workspace && app.expandedProject == p.Path {
		app.expandedProject = ""
		return
	}
	if err := c.changeWorkspace(p.Path); err != nil {
		c.setStatus("Project selection failed: " + err.Error())
		c.setHistoryStatus("History unavailable: " + err.Error())
		return
	}
	app.workspace = c.cwd
	app.projectName = p.Name
	app.projectPath = p.Path
	app.expandedProject = p.Path
	app.section = ""
	app.closeMenus()
}

func (app *chatApp) newThreadIn(p Project) {
	c := app.c
	if err := c.changeWorkspace(p.Path); err != nil {
		c.setStatus(err.Error())
		return
	}
	app.workspace = c.cwd
	app.projectName = p.Name
	app.projectPath = p.Path
	app.expandedProject = p.Path
	app.newThread()
}

// view is the window: the sidebar beside the conversation.
func (app *chatApp) view(ctx *ui.Context) {
	if !app.mobile {
		hub := app.ensureDesktopWorkspaces()
		if hub.active != app {
			hub.active.view(ctx)
			return
		}
		ctx = ctx.Key("workspace:" + hub.store.Active)
	}
	useInterfaceLanguage(ctx)
	app.md.LinkMenu = messageLinkMenu(ctx, app.c.cwd)
	app.markVisibleThreadRead()
	if app.memoryRevisionSeen != app.c.memoryRevision {
		app.memoryRevisionSeen = app.c.memoryRevision
		app.memoryNotes = nil
		app.memories.list.Entries = nil
		app.memories.loadedAt = time.Time{}
	}
	app.applyAppearance()
	ctx.SetTheme(app.theme)
	ui.Row(ctx).Fill().AlignItems(ui.Stretch).Background(canvas).TextColor(ink).FontSize(13).Children(func() {
		if app.sidebar {
			app.sidebarView(ctx)
		}
		ui.Column(ctx).Grow(1).MinWidth(0).FillHeight().Children(func() {
			app.header(ctx)
			if app.section != "" {
				app.sectionView(ctx)
			} else {
				app.chatView(ctx)
			}
		})
		if p := app.panel(); p.visible {
			app.rightPanel(ctx, p)
		}
	})
	app.projectDialogView(ctx)
	if app.workspaceHub != nil {
		app.workspaceHub.dialog(ctx)
	}
	app.conversationSwitcherView(ctx)
	app.shortcutHelpView(ctx)
	app.commandPaletteView(ctx)
	app.registerDesktopShortcuts(ctx)
}

// The account view and composer quota read through the controller's client.
var _ accountCaller = (*codex.Client)(nil)
