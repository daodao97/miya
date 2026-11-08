package desktopui

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
	"miya/internal/prefs"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
	"miya/locales"
)

// chatApp holds what only the interface needs; conversations, connections
// and settings stay in the controller.
type App struct {
	C Host
	*conversation.View

	Sidebar   bool
	Section   string // "", "Settings", "Automations" or "Skills"
	menu      string // the composer's or header's open menu
	modelOpen bool
	effortOpen,
	agentOpen,
	permissionsOpen bool
	Notice         string
	focusComposer  bool
	ShortcutsOpen  bool
	Switcher       conversationSwitcher
	CommandPalette desktopCommandPalette

	Workspace, ProjectName, ProjectPath string
	ExpandedProject                     string
	ProjectDialog                       bool
	ProjectError                        string
	projectBrowse                       bool
	projectBrowseLoading                bool
	projectDirectories                  []svcproto.HostEntry
	threadListExpanded                  map[string]bool
	desktopReadIntent                   appmodel.DesktopRead // a completed result the user chose to view

	answers        map[string]map[string]string // question answers, by approval ID
	forms          map[string]string            // MCP form JSON, by approval ID
	formErrors     map[string]string
	SettingsTab    int
	settingDrafts  map[string]string
	ServiceRestart ServiceRestartUI
	WorkspaceHub   *DesktopWorkspaces

	// appearance is "", "light" or "dark"; dark and themed the palette applied.
	Appearance   string
	Dark, themed bool

	// editing is the user message being edited, 0 for none.
	Editing   int
	EditText  string
	editInput conversation.InlineThreadInput

	Complete           completion
	Memories           MemoryUI
	MemoryNotes        map[string]*MemoryNoticeUI
	MemoryRevisionSeen uint64
	Contacts           widgets.ContactUI
	Autos              automationsUI
	Collaborations     widgets.CollaborationUI
	account            accountView
	Usage              *DesktopUsage
	openWindow         func()
	quitting           bool

	panels       map[int]*panelState
	PanelVisible bool
	panelWidth   float32
	nextSurface  int
}

func newChatApp() *App {
	c := environment.NewWindow()
	app := NewChatAppWithController(c)
	app.EnsureDesktopWorkspaces()
	return app
}

func NewChatAppWithController(c Host) *App {
	app := &App{
		C:                  c,
		View:               conversation.NewConversationView(c),
		Sidebar:            true,
		Workspace:          c.State().Cwd,
		ProjectName:        filepath.Base(c.State().Cwd),
		ProjectPath:        c.State().Cwd,
		ExpandedProject:    c.State().Cwd,
		threadListExpanded: map[string]bool{},
		answers:            map[string]map[string]string{},
		forms:              map[string]string{},
		formErrors:         map[string]string{},
		settingDrafts:      map[string]string{},
		Appearance:         prefs.NormalizeAppearance(prefs.Load("appearance")),
	}
	app.ResolveTitle = app.desktopReferenceTitle
	app.OnRetry = func(req appmodel.RetryRequest) {
		if err := c.RetryResponse(req); err != nil {
			app.Notice = err.Error()
		}
	}
	app.OnHandoff = app.HandoffToNewThread
	app.DesktopAccessory = app.collaborationToggle
	app.UserGroup = app.userGroupView
	app.MemoryNotice = app.memoryNoticeView
	app.PasteAttachments = app.pasteAttachmentsData
	app.SetNotice = func(s string) { app.Notice = s }
	app.NeedsReadFrame = func(t model.Thread, state *ui.ListState, groups int) bool {
		return !app.ProjectDialog && !app.Switcher.Open && !app.ShortcutsOpen && !app.CommandPalette.Open && !t.Busy && !t.Loading && t.HistoryLoaded && groups > 0 && c.DesktopThreadUnread(t) && state.AtEnd()
	}
	app.Md = markdown.Options{
		OnLink:    app.OpenMessageLink,
		LoadImage: func(src string) (ui.ImageSource, error) { return widgets.LoadImage(widgets.ImageSourcePath(src)) },
	}
	if c.RemoteHost() {
		app.ImagePreview = app.remoteMessageImage
		app.Md.LoadImage = app.loadRemoteImage
	}
	app.account.init(app)
	uiruntime.OnCleanup(app.account.stopLogins)
	app.panelWidth = 460
	c.State().CommandsChanged = app.refreshCommandChoices
	uiruntime.OnCleanup(app.closePanels)
	return app
}

func DesktopMain() {
	app := newChatApp()
	widgets.ConfigureInterfaceLanguage()
	mygo.App.OnLocaleChanged(func(tag string) {
		locales.SetCurrent(tag)
		app.activeWorkspaceApp().syncUsageTray()
	})
	mygo.Theme.SetSource(widgets.ThemeSource(app.Appearance))
	// With the background service running, the service serves phones: a
	// window only mirrors the current project.
	app.C.EnsurePhoneHost()
	open := func() {
		if w := uiruntime.CurrentWindow(); w != nil && !w.IsDestroyed() {
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
			Content:              ui.View(app.Render),
		})
		uiruntime.SetAppWindow(w)
		w.OnFocus(func() {
			w.Update(func() {
				active := app.activeWorkspaceApp()
				if active.Section == "" {
					active.RequestDesktopRead(active.C.Current())
					active.MarkVisibleThreadRead()
				}
			})
		})
		w.OnClose(func(e *mygo.CloseEvent) {
			active := app.activeWorkspaceApp()
			if !app.quitting && runtime.GOOS == "darwin" && active.Usage != nil && active.Usage.tray != nil {
				e.PreventDefault()
				w.Hide()
			}
		})
		w.OnClosed(func() {
			if uiruntime.CurrentWindow() == w {
				uiruntime.SetAppWindow(nil)
			}
		})
	}
	app.openWindow = open
	mygo.App.WhenReady(func() {
		widgets.WatchSystemAppearance()
		open()
		app.startUsage()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		active := app.activeWorkspaceApp()
		if !app.quitting && !hasVisibleWindows && (active.Usage == nil || active.Usage.panel == nil) {
			open()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { app.quitting = true; app.activeWorkspaceApp().stopUsage() })
	mygo.App.OnQuit(uiruntime.RunCleanups)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (app *App) project() string { return app.ProjectName }

func (app *App) closeMenus() {
	app.menu = ""
	app.modelOpen, app.effortOpen, app.agentOpen, app.permissionsOpen = false, false, false, false
}

func (app *App) newThread() {
	if t := app.C.Current(); t.Contact != nil && t.Contact.Daily {
		for _, project := range app.C.State().Projects() {
			if err := app.C.ChangeWorkspace(project.Path); err != nil {
				app.Notice = err.Error()
				return
			}
			app.Workspace, app.ProjectPath, app.ProjectName, app.ExpandedProject = project.Path, project.Path, project.Name, project.Path
			break
		}
	}
	app.C.NewThread()
	app.Section = ""
	app.closeMenus()
	app.focusComposer = true
}

func (app *App) SelectThread(t model.Thread) {
	if path := app.C.ThreadWorkspace(t.ID); path != app.C.State().Cwd {
		if err := app.C.ChangeWorkspace(path); err != nil {
			app.C.State().SetStatus("Project selection failed: " + err.Error())
			return
		}
		app.Workspace, app.ProjectPath, app.ExpandedProject = path, path, path
		for _, p := range app.C.State().Projects() {
			if p.Path == path {
				app.ProjectName = p.Name
				break
			}
		}
	}
	app.C.SelectThread(t)
	app.RequestDesktopRead(t)
	app.Section = ""
	app.closeMenus()
	app.focusComposer = true
}

// send runs a slash command or sends the draft.
func (app *App) send() { app.SendDraft(false) }

// sendDraft runs a slash command or sends the draft; while a turn runs, it
// steers it, or queues the draft when steer is false.
func (app *App) SendDraft(steer bool) {
	c := app.C
	// In a Claude Code conversation, the commands Miya does not run itself
	// (skills, custom commands, /compact…) go to Claude Code as messages.
	if command, ok := composer.SlashCommand(c.State().Draft()); ok && !c.CurrentAgentControls().Commands(c.State().Cwd, false).TakesCommand(command) {
		app.Notice = ""
		clear := func() { c.RememberDraft(""); app.menu = "" }
		if name, _, _ := strings.Cut(command, " "); name == "goal" {
			app.Notice = c.RunThreadCommand(command)
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
			app.Section = "Settings"
		case "mention":
			c.RememberDraft("@")
			app.Complete.edit(app, "@")
		case "diff":
			clear()
			app.openSurface(app.Panel(), surfaceDiff)
		case "status":
			clear()
			controls := c.CurrentAgentControls()
			app.Notice = fmt.Sprintf("Model: %s · Thinking: %s · Permissions: %s · Tokens: %d", controls.Model(), controls.Effort(), permissionChoiceTitle(c.CurrentPermissionPolicy()), c.Current().Tokens)
		case "fast", "fast on", "fast off":
			clear()
			app.Notice = c.RunThreadCommand(command)
		case "compact", "review":
			app.Notice = c.RunThreadCommand(command)
		default:
			app.Notice = "Unknown command. Type / to choose a supported command."
		}
		return
	}
	app.Notice = ""
	before := c.State().Active()
	if id := c.SendDraft(steer); id != 0 {
		app.ListFor(id).ScrollToEnd()
	}
	if c.State().LastSubmitError != "" {
		app.Notice = c.State().LastSubmitError
	}
	if before == 0 && c.State().Active() != 0 {
		app.adoptDraftPanel(c.State().Active())
	}
	app.Section = ""
	app.menu = ""
}

func (app *App) attachFiles() {
	cwd := app.C.State().Cwd
	if app.C.RemoteHost() {
		cwd, _ = os.UserHomeDir()
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      uiruntime.CurrentWindow(),
			Title:       tr("Add files"),
			DefaultPath: cwd,
			ButtonLabel: tr("Attach"),
			Multiple:    true,
		})
		if err == nil && len(paths) > 0 {
			app.C.Dispatch(func() { app.C.AddAttachments(paths) })
		}
	}()
}

// pasteAttachments turns files or an image on the clipboard into
// attachments. It reports whether it found any, so that text pastes as text.
func (app *App) pasteAttachmentsData(clipboard transfer.Data) bool {
	if paths, err := clipboard.Files(); err == nil && len(paths) > 0 {
		app.C.AddAttachments(paths)
		return true
	}
	data, err := clipboard.Read(transfer.PNG)
	if err != nil || len(data) == 0 {
		return false
	}
	file, err := os.CreateTemp("", "miya-clipboard-*.png")
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
	app.C.AddAttachments([]string{name})
	return true
}

func (app *App) OpenProjectDialog() {
	app.ProjectPath = app.Workspace
	app.ProjectError = ""
	app.ProjectDialog = true
	app.projectBrowse, app.projectDirectories = false, nil
	app.closeMenus()
}

func (app *App) openWorkspace() {
	if app.C.RemoteHost() {
		app.openSurface(app.Panel(), SurfaceFiles)
		return
	}
	path := app.Workspace
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
			app.C.Dispatch(func() { app.C.State().SetStatus("Unable to open workspace: " + err.Error()) })
		}
	}()
}

func (app *App) browseProject() {
	if app.C.RemoteHost() {
		app.browseServerProject(app.ProjectPath)
		return
	}
	start := app.Workspace
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            uiruntime.CurrentWindow(),
			Title:             tr("Select project directory"),
			DefaultPath:       start,
			ButtonLabel:       tr("Choose"),
			Directory:         true,
			CreateDirectories: true,
		})
		app.C.Dispatch(func() {
			if err != nil {
				app.ProjectError = "Folder selection failed: " + err.Error()
				return
			}
			if len(paths) > 0 {
				app.ProjectPath = paths[0]
				if strings.TrimSpace(app.ProjectName) == "" {
					app.ProjectName = filepath.Base(paths[0])
				}
			}
		})
	}()
}

func (app *App) confirmProject() {
	c := app.C
	path := strings.TrimSpace(app.ProjectPath)
	name := strings.TrimSpace(app.ProjectName)
	if name == "" {
		app.ProjectError = "Enter a project name."
		return
	}
	if err := c.ChangeWorkspace(path); err != nil {
		app.ProjectError = err.Error()
		return
	}
	app.Workspace = c.State().Cwd
	app.ProjectName = name
	app.ExpandedProject = c.State().Cwd
	c.RenameProject(c.State().Cwd, name)
	app.ProjectDialog = false
	app.ProjectError = ""
	app.Section = ""
}

func (app *App) selectProject(p projectmodel.Project) {
	c := app.C
	if p.Path == app.Workspace && app.ExpandedProject == p.Path {
		app.ExpandedProject = ""
		return
	}
	if err := c.ChangeWorkspace(p.Path); err != nil {
		c.State().SetStatus("Project selection failed: " + err.Error())
		c.State().SetHistoryStatus("History unavailable: " + err.Error())
		return
	}
	app.Workspace = c.State().Cwd
	app.ProjectName = p.Name
	app.ProjectPath = p.Path
	app.ExpandedProject = p.Path
	app.Section = ""
	app.closeMenus()
}

func (app *App) newThreadIn(p projectmodel.Project) {
	c := app.C
	if err := c.ChangeWorkspace(p.Path); err != nil {
		c.State().SetStatus(err.Error())
		return
	}
	app.Workspace = c.State().Cwd
	app.ProjectName = p.Name
	app.ProjectPath = p.Path
	app.ExpandedProject = p.Path
	app.newThread()
}

// view is the window: the sidebar beside the conversation.
func (app *App) Render(ctx *ui.Context) {
	if !app.Mobile {
		hub := app.EnsureDesktopWorkspaces()
		if hub.Active != app {
			hub.Active.Render(ctx)
			return
		}
		ctx = ctx.Key("workspace:" + hub.Store.Active)
	}
	widgets.UseInterfaceLanguage(ctx)
	app.Md.LinkMenu = widgets.MessageLinkMenu(ctx, app.C.State().Cwd)
	app.MarkVisibleThreadRead()
	if app.MemoryRevisionSeen != app.C.State().MemoryRevision {
		app.MemoryRevisionSeen = app.C.State().MemoryRevision
		app.MemoryNotes = nil
		app.Memories.List.Entries = nil
		app.Memories.LoadedAt = time.Time{}
	}
	app.ApplyAppearance()
	ctx.SetTheme(app.Theme)
	ui.Row(ctx).Fill().AlignItems(ui.Stretch).Background(widgets.Canvas).TextColor(widgets.Ink).FontSize(13).Children(func() {
		if app.Sidebar {
			app.SidebarView(ctx)
		}
		ui.Column(ctx).Grow(1).MinWidth(0).FillHeight().Children(func() {
			app.header(ctx)
			if app.Section != "" {
				app.sectionView(ctx)
			} else {
				app.chatView(ctx)
			}
		})
		if p := app.Panel(); p.Visible {
			app.rightPanel(ctx, p)
		}
	})
	app.projectDialogView(ctx)
	if app.WorkspaceHub != nil {
		app.WorkspaceHub.dialog(ctx)
	}
	app.conversationSwitcherView(ctx)
	app.shortcutHelpView(ctx)
	app.commandPaletteView(ctx)
	app.registerDesktopShortcuts(ctx)
}

// The account view and composer quota read through the controller's client.
