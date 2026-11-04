package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const localDesktopWorkspace = "local"

type desktopWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link,omitempty"`
}

type desktopWorkspaceStore struct {
	Version int                `json:"version"`
	Active  string             `json:"active"`
	Items   []desktopWorkspace `json:"items"`
}

func defaultDesktopWorkspaces() desktopWorkspaceStore {
	return desktopWorkspaceStore{Version: 1, Active: localDesktopWorkspace, Items: []desktopWorkspace{{ID: localDesktopWorkspace, Name: "Local"}}}
}

func (s desktopWorkspaceStore) find(id string) (desktopWorkspace, bool) {
	for _, item := range s.Items {
		if item.ID == id {
			return item, true
		}
	}
	return desktopWorkspace{}, false
}
func (s desktopWorkspaceStore) current() desktopWorkspace {
	item, _ := s.find(s.Active)
	return item
}
func (s desktopWorkspaceStore) clone() desktopWorkspaceStore {
	s.Items = append([]desktopWorkspace(nil), s.Items...)
	return s
}

func (s *desktopWorkspaceStore) remember(link, name string) (desktopWorkspace, error) {
	id, err := desktopID(link)
	if err != nil {
		return desktopWorkspace{}, err
	}
	id = "remote-" + id
	name = strings.TrimSpace(name)
	for i := range s.Items {
		if s.Items[i].ID == id {
			if name != "" {
				s.Items[i].Name = name
			}
			return s.Items[i], nil
		}
	}
	if name == "" {
		name = fmt.Sprintf("Remote workspace %d", len(s.Items))
	}
	item := desktopWorkspace{ID: id, Name: name, Link: strings.TrimSpace(link)}
	s.Items = append(s.Items, item)
	return item, nil
}

func loadDesktopWorkspaces() (desktopWorkspaceStore, error) {
	store := defaultDesktopWorkspaces()
	data, err := os.ReadFile(remoteServerPath())
	if err == nil {
		var saved desktopWorkspaceStore
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
			return store, errors.New("Unable to read the workspace list")
		}
		for _, item := range saved.Items {
			if item.ID == localDesktopWorkspace {
				continue
			}
			remembered, err := store.remember(item.Link, item.Name)
			if err != nil || remembered.ID != item.ID {
				return defaultDesktopWorkspaces(), errors.New("The saved workspace connection is invalid")
			}
		}
		if _, ok := store.find(saved.Active); ok {
			store.Active = saved.Active
		}
		return store, nil
	}
	if !os.IsNotExist(err) {
		return store, errors.New("Unable to read the workspace list")
	}
	// Migrate the old singleton in memory. The next successful selection
	// saves the registry; an intentional local selection never resurrects it.
	data, err = os.ReadFile(filepath.Join(serviceDir(), "remote-client.json"))
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return store, errors.New("Unable to read the previous server connection")
	}
	var legacy struct {
		Link string `json:"link"`
	}
	if json.Unmarshal(data, &legacy) != nil {
		return store, errors.New("The previous server connection is invalid")
	}
	if legacy.Link != "" {
		item, err := store.remember(legacy.Link, "")
		if err != nil {
			return store, errors.New("The previous server connection is invalid")
		}
		store.Active = item.ID
	}
	return store, nil
}

func (s desktopWorkspaceStore) save() error {
	if err := os.MkdirAll(serviceDir(), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writePrivateFile(remoteServerPath(), data)
}

// Each workspace owns its controller and entire window state. Keeping these
// objects alive also keeps drafts, file edits, terminals and reconnects bound
// to the correct host when two servers reuse conversation numbers or paths.
type desktopWorkspaces struct {
	root, active       *chatApp
	store              desktopWorkspaceStore
	sessions           map[string]*chatApp
	loadErr            error
	err                string
	open, busy, closed bool
	name, link, editID string
	pending            desktopWorkspace
	generation         uint64
	cancel             context.CancelFunc
	connect            func(context.Context, desktopWorkspace) (*serviceClient, svcState, error)
}

func (app *chatApp) ensureDesktopWorkspaces() *desktopWorkspaces {
	if app.workspaceHub != nil {
		return app.workspaceHub
	}
	store, err := loadDesktopWorkspaces()
	m := &desktopWorkspaces{root: app, active: app, store: store, sessions: map[string]*chatApp{store.Active: app}, loadErr: err, connect: connectDesktopWorkspace}
	if err != nil {
		m.err = err.Error()
	}
	m.bind(app)
	onCleanup(m.close)
	return m
}

func (app *chatApp) activeWorkspaceApp() *chatApp {
	if app.workspaceHub != nil && app.workspaceHub.active != nil {
		return app.workspaceHub.active
	}
	return app
}

func (m *desktopWorkspaces) bind(app *chatApp) {
	app.workspaceHub = m
	app.c.workspaceVisible = func() bool { return m.active == app }
	app.c.revealWorkspace = func() {
		for id, session := range m.sessions {
			if session == app {
				m.selectWorkspace(id)
				if m.active == app {
					app.section = ""
				}
				return
			}
		}
	}
}

func connectDesktopWorkspace(ctx context.Context, item desktopWorkspace) (*serviceClient, svcState, error) {
	if item.ID == localDesktopWorkspace {
		return localWindowConnection()
	}
	return connectRemoteServiceContext(ctx, item.Link)
}

func (m *desktopWorkspaces) cancelConnection() {
	m.generation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.busy = false
}

func (m *desktopWorkspaces) selectWorkspace(id string) {
	item, ok := m.store.find(id)
	if !ok || m.closed {
		return
	}
	m.connectWorkspace(item)
}

func (m *desktopWorkspaces) connectWorkspace(item desktopWorkspace) {
	if m.loadErr != nil {
		m.err = m.loadErr.Error()
		return
	}
	m.cancelConnection()
	m.err = ""
	if session := m.sessions[item.ID]; session != nil {
		next := m.store.clone()
		if item.ID != localDesktopWorkspace {
			if _, err := next.remember(item.Link, item.Name); err != nil {
				m.err = err.Error()
				return
			}
		}
		next.Active = item.ID
		if err := next.save(); err != nil {
			m.err = err.Error()
			return
		}
		m.store, m.open = next, false
		m.activate(session)
		return
	}
	if item.ID == localDesktopWorkspace && (runtime.GOOS == "windows" || os.Getenv("MIYA_SERVICE") == "0") {
		next := m.store.clone()
		next.Active = item.ID
		if err := next.save(); err != nil {
			m.err = err.Error()
			return
		}
		app := newChatAppWithController(newLocalWindowController())
		m.store, m.open = next, false
		m.sessions[item.ID] = app
		m.bind(app)
		m.activate(app)
		return
	}
	m.pending, m.busy = item, true
	ctx, cancel := context.WithCancel(m.root.c.ctx)
	m.cancel = cancel
	gen := m.generation
	go func() {
		cl, state, err := m.connect(ctx, item)
		m.root.c.dispatch(func() { m.finishConnection(gen, item, cl, state, err) })
	}()
}

func (m *desktopWorkspaces) finishConnection(gen uint64, item desktopWorkspace, cl *serviceClient, state svcState, err error) {
	if m.closed || gen != m.generation {
		if cl != nil {
			cl.Close()
		}
		return
	}
	m.busy = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if err == nil && cl == nil {
		err = errServiceClosed
	}
	if err != nil {
		if cl != nil {
			cl.Close()
		}
		m.err = err.Error()
		return
	}
	next := m.store.clone()
	if item.ID != localDesktopWorkspace {
		item, err = next.remember(item.Link, item.Name)
	}
	if err == nil {
		next.Active = item.ID
		err = next.save()
	}
	if err != nil {
		cl.Close()
		m.err = err.Error()
		return
	}
	c := newMirrorController()
	c.mirror.remoteHost = item.ID != localDesktopWorkspace
	c.mirror.reconnect = func() (*serviceClient, svcState, error) { return m.connect(c.ctx, item) }
	c.attachService(cl, state)
	app := newChatAppWithController(c)
	m.store, m.open = next, false
	m.sessions[item.ID] = app
	m.bind(app)
	m.activate(app)
}

func (m *desktopWorkspaces) activate(app *chatApp) {
	if m.active == app {
		return
	}
	old := m.active
	if old.usage != nil {
		old.stopUsage()
		old.usage = nil
	}
	m.active = app
	app.sidebar = old.sidebar
	app.openWindow = m.root.openWindow
	app.appearance = normalizeAppearance(loadSetting("appearance"))
	app.themed = false
	if currentWindow() != nil {
		if app.c.remote == nil && desktopPhone == nil {
			desktopPhone = newPhoneHost(app.c)
		}
		app.startUsage()
	}
	invalidate()
}

func (m *desktopWorkspaces) close() {
	if m.closed {
		return
	}
	m.closed = true
	m.cancelConnection()
	for _, app := range m.sessions {
		app.stopUsage()
		app.closePanels()
		if app.c.cancel != nil {
			app.c.cancel()
		}
		if app.c.remote != nil {
			app.c.remote.Close()
		}
	}
}
