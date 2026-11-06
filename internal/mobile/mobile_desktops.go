package mobile

import (
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	conversation "miya/internal/conversation"
	model "miya/internal/model"
	remote "miya/internal/remote"
	uiruntime "miya/internal/uiruntime"
	os "os"
	filepath "path/filepath"
	strings "strings"
	sync "sync"
	atomic "sync/atomic"

	mygo "github.com/egoist/mygo"
	ui "github.com/egoist/mygo/ui"
)

type MobileDesktops struct {
	Version int                      `json:"version"`
	Active  string                   `json:"active"`
	Items   []appmodel.MobileDesktop `json:"items"`
}

type desktopPersistence struct {
	Mu       sync.Mutex
	revision atomic.Uint64
}

func (d *MobileDesktops) Remember(raw, name string) (appmodel.MobileDesktop, error) {
	id, err := appmodel.DesktopID(raw)
	if err != nil {
		return appmodel.MobileDesktop{}, err
	}
	for i := range d.Items {
		if d.Items[i].ID == id {
			if strings.TrimSpace(name) != "" {
				d.Items[i].Name = strings.TrimSpace(name)
			}
			return d.Items[i], nil
		}
	}
	item := appmodel.MobileDesktop{ID: id, Link: strings.TrimSpace(raw), Name: strings.TrimSpace(name)}
	if item.Name == "" {
		item.Name = fmt.Sprintf(tr("Desktop %d"), len(d.Items)+1)
	}
	d.Version = 1
	d.Items = append(d.Items, item)
	return item, nil
}

func (d MobileDesktops) find(id string) (appmodel.MobileDesktop, bool) {
	for _, item := range d.Items {
		if item.ID == id {
			return item, true
		}
	}
	return appmodel.MobileDesktop{}, false
}

// A missing registry migrates the old singleton without changing its link,
// so the desktop's existing cache remains available. An empty saved registry
// is intentional and must not resurrect a removed legacy pairing.
func ReadMobileDesktops(store appmodel.MobileSecretStore) (MobileDesktops, bool, error) {
	d := MobileDesktops{Version: 1}
	if store == nil {
		return d, false, nil
	}
	data, err := store.Get(appmodel.MobileDesktopsKey)
	if err == nil {
		var saved MobileDesktops
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
			return d, false, errors.New(tr("Unable to read saved desktops"))
		}
		for _, item := range saved.Items {
			if _, err := d.Remember(item.Link, item.Name); err != nil {
				return MobileDesktops{Version: 1}, false, errors.New(tr("Invalid saved desktop connection"))
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
	item, err := d.Remember(string(legacy), "")
	if err != nil {
		return d, false, errors.New(tr("Invalid saved desktop connection"))
	}
	d.Active = item.ID
	return d, true, nil
}

func (m *App) SaveDesktops() {
	if !m.Loaded || m.Store == nil || m.desktopLoadFailed {
		return
	}
	m.Desktops.Version = 1
	data, err := json.Marshal(m.Desktops)
	if err != nil {
		return
	}
	store := m.Store
	revision := m.DesktopWrites.revision.Add(1)
	go func() {
		m.DesktopWrites.Mu.Lock()
		defer m.DesktopWrites.Mu.Unlock()
		if revision != m.DesktopWrites.revision.Load() {
			return
		}
		err := store.Set(appmodel.MobileDesktopsKey, data)
		if err == nil {
			// Remove the singleton only after its replacement is durable.
			err = store.Delete("desktop-link")
		}
		uiruntime.RunOnMain(func() {
			if revision != m.DesktopWrites.revision.Load() {
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
func (m *App) restoreDesktops(saved MobileDesktops) {
	for _, item := range m.Desktops.Items {
		if _, exists := saved.find(item.ID); !exists {
			_, _ = saved.Remember(item.Link, item.Name)
		}
	}
	if m.Link != "" {
		name := ""
		if m.Connected {
			name = m.Host
		}
		if item, err := saved.Remember(m.Link, name); err == nil {
			saved.Active = item.ID
			if !m.Connected {
				m.Host = item.Name
			}
		}
	}
	m.Desktops = saved
}

func (m *App) CurrentDesktopID() string {
	id, _ := appmodel.DesktopID(m.Link)
	return id
}

func (m *App) SwitchDesktop(id string) {
	item, ok := m.Desktops.find(id)
	if !ok {
		return
	}
	if item.Link != m.Link || !m.Connected {
		m.Connect(item.Link)
	}
}

func (m *App) RemoveDesktop(id string) {
	item, found := m.Desktops.find(id)
	current := found && item.Link == m.Link || id == m.CurrentDesktopID()
	if !found && !current {
		return
	}
	if current {
		m.DisconnectDesktop(true)
		m.DropDiskCache()
		m.ResetDesktopState()
		m.Link, m.Host, m.Status, m.err = "", "", "", ""
	}
	for i, saved := range m.Desktops.Items {
		if saved.ID == id {
			m.Desktops.Items = append(m.Desktops.Items[:i:i], m.Desktops.Items[i+1:]...)
			break
		}
	}
	if !current && found && m.DataDir != "" {
		m.cacheWork.pending.Wait()
		_ = os.RemoveAll(filepath.Join(m.DataDir, "threads", appmodel.CacheName(item.Link)))
	}
	if session, ok := m.desktopSessions[id]; ok {
		for _, path := range session.Images {
			_ = os.Remove(path)
		}
		delete(m.desktopSessions, id)
	}
	if current {
		m.Desktops.Active = ""
		if len(m.Desktops.Items) > 0 {
			m.Connect(m.Desktops.Items[0].Link)
		} else {
			m.Home()
			m.updateBadge()
		}
	}
	if store, key := m.Store, DesktopUnlockKey(id); store != nil && key != "" {
		gen := m.Generation
		go func() {
			m.DesktopWrites.Mu.Lock()
			err := store.Delete(key)
			m.DesktopWrites.Mu.Unlock()
			if err != nil {
				m.onMain(gen, func() {
					m.desktopError = tr("Connection removed, but the login password could not be deleted from Keychain")
					uiruntime.Invalidate()
				})
			}
		}()
	}
	m.SaveDesktops()
}

func (m *App) ResetDesktopState() {
	m.clearFileReferences()
	m.clearWebServices()
	m.WebProject, m.WebPort, m.WebManual = "", "", false
	m.WebRef = appmodel.ThreadRef{}
	m.WebDetails = false
	m.ConversationMenu = false
	m.Home()
	m.State, m.Host, m.Selected = appmodel.PhoneState{}, "", ""
	m.Autos = mobileAutomations{}
	m.Memories = MobileMemory{}
	m.Options, m.optionsAgent, m.NewAgent = nil, "", ""
	m.DesktopNotice = ""
	m.Commands, m.Asked, m.PreviewPaths = nil, nil, nil
	m.approvalID, m.ApprovalFields, m.ApprovalPending, m.readPending = nil, nil, nil, nil
	m.BadgeRevision = 0
	m.pushOpen, m.pushOpenRef = 0, remote.Request{}
	m.ExpandedProject, m.projectsExpanded, m.ThreadListExpanded = "", false, nil
	m.DrawerSearch, m.DrawerQuery, m.Menu, m.QuotaOpen = false, "", false, false
	m.DrawerOffset, m.DrawerDragging, m.DrawerVisible = ui.Point{}, false, false
	m.quotaDrawerShown, m.rowPressed, m.pressOpen = false, false, false
	m.Viewer = newMobileViewer()
	m.View.C = m.Viewer
	m.View.Groups = conversation.GroupCache{}
	m.View.Lists = map[int]*ui.ListState{}
	m.View.Copied, m.View.Open = map[int]bool{}, map[int]bool{}

	m.FormErrors = map[string]string{}

	m.InlineInput = conversation.InlineThreadInput{}
}

func (m *App) saveDesktopSession() {
	id := m.CurrentDesktopID()
	if id == "" {
		return
	}
	if m.desktopSessions == nil {
		m.desktopSessions = map[string]appmodel.MobileDesktopSession{}
	}
	thread := m.Viewer.Current()
	if thread.ServerID != "" && len(thread.Messages) > 0 {
		m.Remember(thread.ServerID, thread.Messages)
	}
	m.desktopSessions[id] = appmodel.MobileDesktopSession{Thread: appmodel.ListThread(thread), Selected: m.Selected, Agent: m.NewAgent,
		Draft: m.Draft, Images: append([]string(nil), m.Images...), ExpandedProject: m.ExpandedProject,
		ProjectsExpanded: m.projectsExpanded, ThreadListExpanded: m.ThreadListExpanded}
	m.Images = nil
}

func (m *App) restoreDesktopSession(id string) {
	session, ok := m.desktopSessions[id]
	if !ok {
		return
	}
	delete(m.desktopSessions, id)
	m.Selected, m.NewAgent = session.Selected, session.Agent
	m.ExpandedProject, m.projectsExpanded, m.ThreadListExpanded = session.ExpandedProject, session.ProjectsExpanded, session.ThreadListExpanded
	thread := session.Thread
	if thread.ID != 0 && thread.ServerID != "" {
		for _, project := range m.State.Projects {
			for _, saved := range project.Threads {
				if saved.ServerID == thread.ServerID && model.ThreadAgent(saved) == model.ThreadAgent(thread) {
					thread = saved
				}
			}
		}
		m.OpenThread(thread)
	}
	m.Draft, m.Images = session.Draft, session.Images
}

// Older desktop versions omit a desktop identifier from notifications.
// Resolve their durable conversation identity against the separate caches;
// never open a coincidentally equal runtime thread ID on another desktop.
func (m *App) desktopForNotification(target remote.Request) string {
	if len(m.Desktops.Items) <= 1 {
		return m.CurrentDesktopID()
	}
	if target.ServerID == "" {
		return ""
	}
	match := ""
	for _, desktop := range m.Desktops.Items {
		state := m.State
		if desktop.Link != m.Link {
			var saved appmodel.PhoneSavedState
			if m.DataDir == "" || appmodel.ReadGzipJSON(filepath.Join(m.DataDir, "threads", appmodel.CacheName(desktop.Link), appmodel.StateFile), &saved) != nil {
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

func (m *App) OpenNotification(data map[string]string) {
	target := notificationTarget(data)
	id := m.desktopForNotification(target)
	if len(m.Desktops.Items) > 1 && id == "" {
		m.DesktopNotice = tr("Select the desktop for this notification")
		m.openDesktopManager()
		return
	}
	if id != "" && id != m.CurrentDesktopID() {
		m.SwitchDesktop(id)
	}
	m.pushOpenRef, m.pushOpen = target, target.Thread
	m.openPushThread()
}
