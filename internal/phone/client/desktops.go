package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/chat/model"
	phonecache "miya/internal/phone/cache"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/service/workspaces"
)

type MobileDesktops struct {
	Version int                  `json:"version"`
	Active  string               `json:"active"`
	Items   []phonecache.Desktop `json:"items"`
}

func (d *MobileDesktops) Remember(raw, name string) (phonecache.Desktop, error) {
	id, err := workspaces.IDFromLink(raw)
	if err != nil {
		return phonecache.Desktop{}, err
	}
	for i := range d.Items {
		if d.Items[i].ID == id {
			if strings.TrimSpace(name) != "" && !d.Items[i].Renamed {
				d.Items[i].Name = strings.TrimSpace(name)
			}
			return d.Items[i], nil
		}
	}
	item := phonecache.Desktop{ID: id, Link: strings.TrimSpace(raw), Name: strings.TrimSpace(name)}
	if item.Name == "" {
		item.Name = fmt.Sprintf(tr("Desktop %d"), len(d.Items)+1)
	}
	d.Version = 1
	d.Items = append(d.Items, item)
	return item, nil
}

// restore re-adds a saved entry, keeping a name the person chose.
func (d *MobileDesktops) restore(item phonecache.Desktop) error {
	saved, err := d.Remember(item.Link, item.Name)
	if err == nil && item.Renamed {
		d.Rename(saved.ID, item.Name)
	}
	return err
}

// Rename sets a person-chosen name. An empty name hands the entry back to the
// host name the desktop reports on its next connection.
func (d *MobileDesktops) Rename(id, name string) bool {
	name = strings.TrimSpace(name)
	for i := range d.Items {
		if d.Items[i].ID == id {
			d.Items[i].Renamed = name != ""
			if name != "" {
				d.Items[i].Name = name
			}
			return true
		}
	}
	return false
}

func (d MobileDesktops) find(id string) (phonecache.Desktop, bool) {
	for _, item := range d.Items {
		if item.ID == id {
			return item, true
		}
	}
	return phonecache.Desktop{}, false
}

// A missing registry migrates the old singleton without changing its link,
// so the desktop's existing cache remains available. An empty saved registry
// is intentional and must not resurrect a removed legacy pairing.
func ReadDesktops(store phonecache.SecretStore, notFound error) (MobileDesktops, error) {
	d := MobileDesktops{Version: 1}
	if store == nil {
		return d, nil
	}
	data, err := store.Get(phonecache.DesktopsKey)
	if err == nil {
		var saved MobileDesktops
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 {
			return d, errors.New(tr("Unable to read saved desktops"))
		}
		for _, item := range saved.Items {
			if err := d.restore(item); err != nil {
				return MobileDesktops{Version: 1}, errors.New(tr("Invalid saved desktop connection"))
			}
		}
		d.Active = saved.Active
		if _, ok := d.find(d.Active); !ok {
			d.Active = ""
			if len(d.Items) > 0 {
				d.Active = d.Items[0].ID
			}
		}
		return d, nil
	}
	if !errors.Is(err, notFound) {
		return d, errors.New(tr("Unlock your phone and reload desktops"))
	}
	legacy, err := store.Get("nsgo-desktop-link")
	if errors.Is(err, notFound) {
		legacy, err = store.Get("desktop-link")
	}
	if err == nil && len(legacy) == 0 {
		return d, nil
	}
	if errors.Is(err, notFound) {
		return d, nil
	}
	if err != nil {
		return d, errors.New(tr("Unlock your phone and reload the desktop connection"))
	}
	item, err := d.Remember(string(legacy), "")
	if err != nil {
		return d, errors.New(tr("Invalid saved desktop connection"))
	}
	d.Active = item.ID
	return d, nil
}

func (m *Client) SaveDesktops() {
	if m.Store == nil || m.desktopLoadFailed {
		return
	}
	m.Desktops.Version = 1
	data, err := json.Marshal(m.Desktops)
	if err != nil {
		return
	}
	store, revision := m.Store, m.storeRevision.Add(1)
	go func() {
		m.storeMu.Lock()
		defer m.storeMu.Unlock()
		if revision != m.storeRevision.Load() {
			return
		}
		err := store.Set(phonecache.DesktopsKey, data)
		if err == nil {
			err = store.Set("nsgo-desktop-link", nil)
		}
		m.post(func() {
			if revision == m.storeRevision.Load() {
				if err != nil {
					m.PersistenceError = tr("Unable to save desktops. Try again.")
				} else {
					m.PersistenceError = ""
				}
			}
		})
	}()
}
func (m *Client) restoreDesktops(saved MobileDesktops) {
	for _, d := range m.Desktops.Items {
		_ = saved.restore(d)
	}
	if m.Link != "" {
		d, _ := saved.Remember(m.Link, m.Host)
		saved.Active = d.ID
	}
	m.Desktops = saved
}
func (m *Client) DesktopName() string {
	for _, d := range m.Desktops.Items {
		if d.Link == m.Link {
			return d.Name
		}
	}
	return m.Host
}
func (m *Client) CurrentDesktopID() string { id, _ := workspaces.IDFromLink(m.Link); return id }
func (m *Client) RenameDesktop(id, name string) {
	if m.Desktops.Rename(id, name) {
		if id == m.CurrentDesktopID() && strings.TrimSpace(name) == "" {
			_, _ = m.Desktops.Remember(m.Link, m.Host)
		}
		m.SaveDesktops()
	}
}
func (m *Client) SwitchDesktop(id string) {
	if d, ok := m.Desktops.find(id); ok && (d.Link != m.Link || !m.Connected) {
		m.Connect(d.Link)
	}
}
func (m *Client) RemoveDesktop(id string) {
	item, found := m.Desktops.find(id)
	if !found {
		return
	}
	current := item.Link == m.Link
	if current {
		m.CloseDesktopPage(false)
		m.PersistState()
		m.FlushCache(true)
		m.Disconnect()
		m.DropDiskCache()
		m.Link, m.Host, m.Status = "", "", ""
		m.State = phoneproto.State{}
		m.Outbox = nil
		m.pendingProjection = nil
		m.Selected, m.NewAgent, m.Draft = "", "", ""
		m.Options, m.Commands, m.Asked = nil, nil, nil
		m.Viewer = NewViewer()
		m.Viewer.Changed = m.change
		m.Home()
	}
	for i, d := range m.Desktops.Items {
		if d.ID == id {
			m.Desktops.Items = append(m.Desktops.Items[:i:i], m.Desktops.Items[i+1:]...)
			break
		}
	}
	if session, ok := m.desktopSessions[id]; ok {
		for _, p := range session.Images {
			_ = os.Remove(p)
		}
		delete(m.desktopSessions, id)
	}
	if !current && m.DataDir != "" {
		m.cacheWork.pending.Wait()
		_ = os.RemoveAll(filepath.Join(m.DataDir, "threads", workspaces.CacheName(item.Link)))
	}
	if current {
		m.Desktops.Active = ""
		if len(m.Desktops.Items) > 0 {
			m.Connect(m.Desktops.Items[0].Link)
		}
	}
	if m.Store != nil {
		store := m.Store
		go func() {
			m.storeMu.Lock()
			err := store.Delete(DesktopUnlockKey(id))
			m.storeMu.Unlock()
			if err != nil {
				m.post(func() {
					m.PersistenceError = tr("Connection removed, but the login password could not be deleted from Keychain")
				})
			}
		}()
	}
	m.SaveDesktops()
}
func (m *Client) saveDesktopSession() {
	id := m.CurrentDesktopID()
	if id == "" {
		return
	}
	if m.desktopSessions == nil {
		m.desktopSessions = map[string]phonecache.Session{}
	}
	t := m.Viewer.Current()
	if t.ServerID != "" && len(t.Messages) > 0 {
		m.Remember(t.ServerID, t.Messages)
	}
	m.desktopSessions[id] = phonecache.Session{Thread: model.ListThread(t), Selected: m.Selected, Agent: m.NewAgent, Draft: m.Draft, Images: append([]string(nil), m.Images...), ExpandedProject: m.ExpandedProject, ThreadListExpanded: m.ThreadListExpanded}
	m.Images = nil
}
func (m *Client) restoreDesktopSession(id string) {
	s, ok := m.desktopSessions[id]
	if !ok {
		return
	}
	delete(m.desktopSessions, id)
	m.Selected, m.NewAgent, m.ExpandedProject, m.ThreadListExpanded = s.Selected, s.Agent, s.ExpandedProject, s.ThreadListExpanded
	if s.Thread.ID != 0 && s.Thread.ServerID != "" {
		m.OpenThread(s.Thread)
	}
	m.Draft, m.Images = s.Draft, s.Images
}

// Retry storage without replacing any pairing entered while the read runs.
func (m *Client) ReloadDesktops() {
	if m.Store == nil {
		return
	}
	store, notFound := m.Store, m.SecretNotFound
	go func() {
		saved, err := ReadDesktops(store, notFound)
		m.post(func() {
			if err != nil {
				m.PersistenceError = err.Error()
				return
			}
			m.desktopLoadFailed = false
			m.PersistenceError = ""
			m.restoreDesktops(saved)
			m.SaveDesktops()
		})
	}()
}
