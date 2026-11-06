package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/model"
	"miya/internal/remote"
)

const mobileDesktopsKey = "desktops-v1"

type mobileSecretStore interface {
	Get(string) ([]byte, error)
	Set(string, []byte) error
	Delete(string) error
}

// Links contain pairing secrets. The complete registry lives in Keychain;
// its opaque IDs, rather than links, identify controls and notifications.
type mobileDesktop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}

type mobileDesktops struct {
	Version int             `json:"version"`
	Active  string          `json:"active"`
	Items   []mobileDesktop `json:"items"`
}

// Retain the phone's in-progress composer when switching workspaces, keeping
// attachments owned by that desktop instead of deleting them on navigation.
type mobileDesktopSession struct {
	Thread             Thread
	Selected, Agent    string
	Draft              string
	Images             []string
	ExpandedProject    string
	ProjectsExpanded   bool
	ThreadListExpanded map[string]bool
}

type desktopPersistence struct {
	mu       sync.Mutex
	revision atomic.Uint64
}

func (d *mobileDesktops) remember(raw, name string) (mobileDesktop, error) {
	id, err := desktopID(raw)
	if err != nil {
		return mobileDesktop{}, err
	}
	for i := range d.Items {
		if d.Items[i].ID == id {
			if strings.TrimSpace(name) != "" {
				d.Items[i].Name = strings.TrimSpace(name)
			}
			return d.Items[i], nil
		}
	}
	item := mobileDesktop{ID: id, Link: strings.TrimSpace(raw), Name: strings.TrimSpace(name)}
	if item.Name == "" {
		item.Name = fmt.Sprintf(tr("Desktop %d"), len(d.Items)+1)
	}
	d.Version = 1
	d.Items = append(d.Items, item)
	return item, nil
}

func (d mobileDesktops) find(id string) (mobileDesktop, bool) {
	for _, item := range d.Items {
		if item.ID == id {
			return item, true
		}
	}
	return mobileDesktop{}, false
}

// A missing registry migrates the old singleton without changing its link,
// so the desktop's existing cache remains available. An empty saved registry
// is intentional and must not resurrect a removed legacy pairing.
func readMobileDesktops(store mobileSecretStore) (mobileDesktops, bool, error) {
	d := mobileDesktops{Version: 1}
	if store == nil {
		return d, false, nil
	}
	data, err := store.Get(mobileDesktopsKey)
	if err == nil {
		var saved mobileDesktops
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
			return d, false, errors.New(tr("Unable to read saved desktops"))
		}
		for _, item := range saved.Items {
			if _, err := d.remember(item.Link, item.Name); err != nil {
				return mobileDesktops{Version: 1}, false, errors.New(tr("Invalid saved desktop connection"))
			}
		}
		d.Active = saved.Active
		if _, ok := d.find(d.Active); !ok {
			d.Active = ""
			if len(d.Items) > 0 {
				d.Active = d.Items[0].ID
			}
		}
		return d, false, nil
	}
	if !errors.Is(err, mygo.ErrSecretNotFound) {
		return d, false, errors.New(tr("Unlock your phone and reload desktops"))
	}
	legacy, err := store.Get("desktop-link")
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return d, false, nil
	}
	if err != nil {
		return d, false, errors.New(tr("Unlock your phone and reload the desktop connection"))
	}
	item, err := d.remember(string(legacy), "")
	if err != nil {
		return d, false, errors.New(tr("Invalid saved desktop connection"))
	}
	d.Active = item.ID
	return d, true, nil
}

func (m *mobileApp) saveDesktops() {
	if !m.loaded || m.store == nil || m.desktopLoadFailed {
		return
	}
	m.desktops.Version = 1
	data, err := json.Marshal(m.desktops)
	if err != nil {
		return
	}
	store := m.store
	revision := m.desktopWrites.revision.Add(1)
	go func() {
		m.desktopWrites.mu.Lock()
		defer m.desktopWrites.mu.Unlock()
		if revision != m.desktopWrites.revision.Load() {
			return
		}
		err := store.Set(mobileDesktopsKey, data)
		if err == nil {
			// Remove the singleton only after its replacement is durable.
			err = store.Delete("desktop-link")
		}
		runOnMain(func() {
			if revision != m.desktopWrites.revision.Load() {
				return
			}
			m.desktopError = ""
			if err != nil {
				m.desktopError = tr("Unable to save desktops. Try again.")
			}
		})
	}()
}

// Restore saved pairings without losing a link opened while Keychain was
// being read during launch.
func (m *mobileApp) restoreDesktops(saved mobileDesktops) {
	for _, item := range m.desktops.Items {
		if _, exists := saved.find(item.ID); !exists {
			_, _ = saved.remember(item.Link, item.Name)
		}
	}
	if m.link != "" {
		name := ""
		if m.connected {
			name = m.host
		}
		if item, err := saved.remember(m.link, name); err == nil {
			saved.Active = item.ID
			if !m.connected {
				m.host = item.Name
			}
		}
	}
	m.desktops = saved
}

func (m *mobileApp) currentDesktopID() string {
	id, _ := desktopID(m.link)
	return id
}

func (m *mobileApp) switchDesktop(id string) {
	item, ok := m.desktops.find(id)
	if !ok {
		return
	}
	if item.Link != m.link || !m.connected {
		m.connect(item.Link)
	}
}

func (m *mobileApp) removeDesktop(id string) {
	item, found := m.desktops.find(id)
	current := found && item.Link == m.link || id == m.currentDesktopID()
	if !found && !current {
		return
	}
	if current {
		m.disconnectDesktop(true)
		m.dropDiskCache()
		m.resetDesktopState()
		m.link, m.host, m.status, m.err = "", "", "", ""
	}
	for i, saved := range m.desktops.Items {
		if saved.ID == id {
			m.desktops.Items = append(m.desktops.Items[:i:i], m.desktops.Items[i+1:]...)
			break
		}
	}
	if !current && found && m.dataDir != "" {
		m.cacheWork.pending.Wait()
		_ = os.RemoveAll(filepath.Join(m.dataDir, "threads", cacheName(item.Link)))
	}
	if session, ok := m.desktopSessions[id]; ok {
		for _, path := range session.Images {
			_ = os.Remove(path)
		}
		delete(m.desktopSessions, id)
	}
	if current {
		m.desktops.Active = ""
		if len(m.desktops.Items) > 0 {
			m.connect(m.desktops.Items[0].Link)
		} else {
			m.home()
			m.updateBadge()
		}
	}
	if store, key := m.store, desktopUnlockKey(id); store != nil && key != "" {
		gen := m.generation
		go func() {
			m.desktopWrites.mu.Lock()
			err := store.Delete(key)
			m.desktopWrites.mu.Unlock()
			if err != nil {
				m.onMain(gen, func() {
					m.desktopError = tr("Connection removed, but the login password could not be deleted from Keychain")
					invalidate()
				})
			}
		}()
	}
	m.saveDesktops()
}

func (m *mobileApp) resetDesktopState() {
	m.clearFileReferences()
	m.clearWebServices()
	m.webProject, m.webPort, m.webManual = "", "", false
	m.webRef = ThreadRef{}
	m.webDetails = false
	m.conversationMenu = false
	m.home()
	m.state, m.host, m.selected = phoneState{}, "", ""
	m.autos = mobileAutomations{}
	m.memories = mobileMemory{}
	m.options, m.optionsAgent, m.newAgent = nil, "", ""
	m.desktopNotice = ""
	m.commands, m.asked, m.previewPaths = nil, nil, nil
	m.approvalID, m.approvalFields, m.approvalPending, m.readPending = nil, nil, nil, nil
	m.badgeRevision = 0
	m.pushOpen, m.pushOpenRef = 0, remote.Request{}
	m.expandedProject, m.projectsExpanded, m.threadListExpanded = "", false, nil
	m.drawerSearch, m.drawerQuery, m.menu, m.quotaOpen = false, "", false, false
	m.drawerOffset, m.drawerDragging, m.drawerVisible = ui.Point{}, false, false
	m.quotaDrawerShown, m.rowPressed, m.pressOpen = false, false, false
	if m.view.c.cancel != nil {
		m.view.c.cancel()
	}
	m.view.c = newViewerController()
	m.view.groups = groupCache{}
	m.view.lists = map[int]*ui.ListState{}
	m.view.copied, m.view.open = map[int]bool{}, map[int]bool{}
	m.view.answers = map[string]map[string]string{}
	m.view.forms, m.view.formErrors = map[string]string{}, map[string]string{}
	m.view.editing, m.view.editText, m.view.menu = 0, "", ""
	m.view.editInput = inlineThreadInput{}
	m.inlineInput = inlineThreadInput{}
}

func (m *mobileApp) saveDesktopSession() {
	id := m.currentDesktopID()
	if id == "" {
		return
	}
	if m.desktopSessions == nil {
		m.desktopSessions = map[string]mobileDesktopSession{}
	}
	thread := m.view.c.current()
	if thread.ServerID != "" && len(thread.Messages) > 0 {
		m.remember(thread.ServerID, thread.Messages)
	}
	m.desktopSessions[id] = mobileDesktopSession{Thread: listThread(thread), Selected: m.selected, Agent: m.newAgent,
		Draft: m.draft, Images: append([]string(nil), m.images...), ExpandedProject: m.expandedProject,
		ProjectsExpanded: m.projectsExpanded, ThreadListExpanded: m.threadListExpanded}
	m.images = nil
}

func (m *mobileApp) restoreDesktopSession(id string) {
	session, ok := m.desktopSessions[id]
	if !ok {
		return
	}
	delete(m.desktopSessions, id)
	m.selected, m.newAgent = session.Selected, session.Agent
	m.expandedProject, m.projectsExpanded, m.threadListExpanded = session.ExpandedProject, session.ProjectsExpanded, session.ThreadListExpanded
	thread := session.Thread
	if thread.ID != 0 && thread.ServerID != "" {
		for _, project := range m.state.Projects {
			for _, saved := range project.Threads {
				if saved.ServerID == thread.ServerID && model.ThreadAgent(saved) == model.ThreadAgent(thread) {
					thread = saved
				}
			}
		}
		m.openThread(thread)
	}
	m.draft, m.images = session.Draft, session.Images
}

func migrateDesktopOptions(dataDir, link string) {
	if dataDir == "" || link == "" {
		return
	}
	destination := filepath.Join(dataDir, "threads", cacheName(link), "message-options.json")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return
	}
	if data, err := os.ReadFile(filepath.Join(dataDir, "message-options.json")); err == nil {
		if os.MkdirAll(filepath.Dir(destination), 0700) == nil {
			_ = os.WriteFile(destination, data, 0600)
		}
	}
}

// Older desktop versions omit a desktop identifier from notifications.
// Resolve their durable conversation identity against the separate caches;
// never open a coincidentally equal runtime thread ID on another desktop.
func (m *mobileApp) desktopForNotification(target remote.Request) string {
	if len(m.desktops.Items) <= 1 {
		return m.currentDesktopID()
	}
	if target.ServerID == "" {
		return ""
	}
	match := ""
	for _, desktop := range m.desktops.Items {
		state := m.state
		if desktop.Link != m.link {
			var saved phoneSavedState
			if m.dataDir == "" || readGzipJSON(filepath.Join(m.dataDir, "threads", cacheName(desktop.Link), stateFile), &saved) != nil {
				continue
			}
			state = saved.State
		}
		found := false
		for _, project := range state.Projects {
			for _, thread := range project.Threads {
				if thread.ServerID == target.ServerID && (target.Agent == "" || model.ThreadAgent(thread) == target.Agent) && (target.Project == "" || project.Path == target.Project) {
					found = true
				}
			}
		}
		if found {
			if match != "" {
				return ""
			}
			match = desktop.ID
		}
	}
	return match
}

func (m *mobileApp) openNotification(data map[string]string) {
	target := notificationTarget(data)
	id := m.desktopForNotification(target)
	if len(m.desktops.Items) > 1 && id == "" {
		m.desktopNotice = tr("Select the desktop for this notification")
		m.openDesktopManager()
		return
	}
	if id != "" && id != m.currentDesktopID() {
		m.switchDesktop(id)
	}
	m.pushOpenRef, m.pushOpen = target, target.Thread
	m.openPushThread()
}
