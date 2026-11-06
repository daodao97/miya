package desktopui

import (
	"context"
	"os"
	"runtime"

	"miya/internal/prefs"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
	"miya/internal/workspaces"
)

// Each workspace owns its controller and entire window state. Keeping these
// objects alive also keeps drafts, file edits, terminals and reconnects bound
// to the correct host when two servers reuse conversation numbers or paths.
type DesktopWorkspaces struct {
	root, Active       *App
	Store              workspaces.Store
	Sessions           map[string]*App
	loadErr            error
	Err                string
	Open, Busy, closed bool
	name, link, editID string
	pending            workspaces.Workspace
	generation         uint64
	cancel             context.CancelFunc
	Connect            func(context.Context, workspaces.Workspace) (*svcproto.Client, svcproto.State, error)
}

func (app *App) EnsureDesktopWorkspaces() *DesktopWorkspaces {
	if app.WorkspaceHub != nil {
		return app.WorkspaceHub
	}
	store, err := workspaces.Load()
	m := &DesktopWorkspaces{root: app, Active: app, Store: store, Sessions: map[string]*App{store.Active: app}, loadErr: err, Connect: connectDesktopWorkspace}
	if err != nil {
		m.Err = err.Error()
	}
	m.bind(app)
	uiruntime.OnCleanup(m.Close)
	return m
}

func (app *App) activeWorkspaceApp() *App {
	if app.WorkspaceHub != nil && app.WorkspaceHub.Active != nil {
		return app.WorkspaceHub.Active
	}
	return app
}

func (m *DesktopWorkspaces) bind(app *App) {
	app.WorkspaceHub = m
	app.C.State().WorkspaceVisible = func() bool { return m.Active == app }
	app.C.State().RevealWorkspace = func() {
		for id, session := range m.Sessions {
			if session == app {
				m.SelectWorkspace(id)
				if m.Active == app {
					app.Section = ""
				}
				return
			}
		}
	}
}

func connectDesktopWorkspace(ctx context.Context, item workspaces.Workspace) (*svcproto.Client, svcproto.State, error) {
	if item.ID == workspaces.Local {
		return environment.LocalConnection()
	}
	return workspaces.ConnectRemoteServiceContext(ctx, item.Link)
}

func (m *DesktopWorkspaces) CancelConnection() {
	m.generation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.Busy = false
}

func (m *DesktopWorkspaces) SelectWorkspace(id string) {
	item, ok := m.Store.Find(id)
	if !ok || m.closed {
		return
	}
	m.ConnectWorkspace(item)
}

func (m *DesktopWorkspaces) ConnectWorkspace(item workspaces.Workspace) {
	if m.loadErr != nil {
		m.Err = m.loadErr.Error()
		return
	}
	m.CancelConnection()
	m.Err = ""
	if session := m.Sessions[item.ID]; session != nil {
		next := m.Store.Clone()
		if item.ID != workspaces.Local {
			if _, err := next.Remember(item.Link, item.Name); err != nil {
				m.Err = err.Error()
				return
			}
		}
		next.Active = item.ID
		if err := next.Save(); err != nil {
			m.Err = err.Error()
			return
		}
		m.Store, m.Open = next, false
		m.activate(session)
		return
	}
	if item.ID == workspaces.Local && (runtime.GOOS == "windows" || os.Getenv("MIYA_SERVICE") == "0") {
		next := m.Store.Clone()
		next.Active = item.ID
		if err := next.Save(); err != nil {
			m.Err = err.Error()
			return
		}
		app := NewChatAppWithController(environment.NewLocal())
		m.Store, m.Open = next, false
		m.Sessions[item.ID] = app
		m.bind(app)
		m.activate(app)
		return
	}
	m.pending, m.Busy = item, true
	ctx, cancel := context.WithCancel(m.root.C.State().Ctx)
	m.cancel = cancel
	gen := m.generation
	go func() {
		cl, state, err := m.Connect(ctx, item)
		m.root.C.Dispatch(func() { m.finishConnection(gen, item, cl, state, err) })
	}()
}

func (m *DesktopWorkspaces) finishConnection(gen uint64, item workspaces.Workspace, cl *svcproto.Client, state svcproto.State, err error) {
	if m.closed || gen != m.generation {
		if cl != nil {
			cl.Close()
		}
		return
	}
	m.Busy = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if err == nil && cl == nil {
		err = svcproto.ErrClosed
	}
	if err != nil {
		if cl != nil {
			cl.Close()
		}
		m.Err = err.Error()
		return
	}
	next := m.Store.Clone()
	if item.ID != workspaces.Local {
		item, err = next.Remember(item.Link, item.Name)
	}
	if err == nil {
		next.Active = item.ID
		err = next.Save()
	}
	if err != nil {
		cl.Close()
		m.Err = err.Error()
		return
	}
	c := environment.NewMirror()
	c.ConfigureMirror(item.ID != workspaces.Local, func() (*svcproto.Client, svcproto.State, error) { return m.Connect(c.State().Ctx, item) })
	c.AttachService(cl, state)
	app := NewChatAppWithController(c)
	m.Store, m.Open = next, false
	m.Sessions[item.ID] = app
	m.bind(app)
	m.activate(app)
}

func (m *DesktopWorkspaces) activate(app *App) {
	if m.Active == app {
		return
	}
	old := m.Active
	if old.Usage != nil {
		old.stopUsage()
		old.Usage = nil
	}
	m.Active = app
	app.Sidebar = old.Sidebar
	app.openWindow = m.root.openWindow
	app.Appearance = prefs.NormalizeAppearance(prefs.Load("appearance"))
	app.themed = false
	if uiruntime.CurrentWindow() != nil {
		app.C.EnsurePhoneHost()
		app.startUsage()
	}
	uiruntime.Invalidate()
}

func (m *DesktopWorkspaces) Close() {
	if m.closed {
		return
	}
	m.closed = true
	m.CancelConnection()
	for _, app := range m.Sessions {
		app.stopUsage()
		app.closePanels()
		if app.C.State().Cancel != nil {
			app.C.State().Cancel()
		}
		if app.C.State().Remote != nil {
			app.C.State().Remote.Close()
		}
	}
}
