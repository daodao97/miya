// Package client holds the phone's desktop connection and conversation state.
// It has no UI framework dependency. The NSGo adapter dispatches changes onto
// its engine; the legacy MyGo surface remains in internal/mobile unchanged.
package client

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"miya/internal/chat/model"
	"miya/internal/notification"
	phonecache "miya/internal/phone/cache"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"

	"miya/locales"
)

func tr(s string) string { return locales.T(s) }

var ErrIncompatible = errors.New(tr("Incompatible desktop version. Update both the mobile and desktop apps."))

// Client is owned by the UI goroutine. Network and disk workers publish only
// through Dispatch, and connection generations reject stale callbacks.
type Client struct {
	Dispatch func(func())
	Changed  func()
	// SendRequest is an optional transport adapter, also useful for protocol tests.
	SendRequest func(remote.Request) error
	DialDesktop func(context.Context, string) (*remote.Conn, error)
	// PrepareNetwork is an optional platform warm-up/permission hook. Nil uses
	// the transport directly. It must honor cancellation.
	PrepareNetwork                                                func(context.Context, string) error
	Store                                                         phonecache.SecretStore
	PersistenceError                                              string
	storeMu                                                       sync.Mutex
	storeRevision                                                 atomic.Uint64
	DataDir, CacheDir                                             string
	Loaded                                                        bool
	Path, Link, Host, Status, Error                               string
	State                                                         phoneproto.State
	Selected, ExpandedProject, Title, NewProject, NewAgent, Draft string
	ThreadListExpanded                                            map[string]bool
	Open                                                          int
	Menu                                                          bool
	Images                                                        []string
	Viewer                                                        *Viewer
	Conn                                                          *remote.Conn
	Cancel                                                        context.CancelFunc
	Generation                                                    int
	Connected                                                     bool
	Wake                                                          chan struct{}
	RetryAt                                                       time.Time
	RetryError                                                    string
	background                                                    bool
	net                                                           mobileNetworkPath
	Options                                                       map[string]remote.SendOptions
	Commands                                                      map[string][]model.ComposerChoice
	Asked                                                         map[string]bool
	FileRefs                                                      *MobileFileReferences
	PreviewPaths                                                  map[string]string
	PreviewRetries                                                map[string]int
	MsgCache                                                      map[string][]model.Message
	msgKeys                                                       []string
	Dirty                                                         map[string]any
	cacheWork                                                     mobileCacheWork
	flushTimer                                                    *time.Timer
	Sending                                                       bool
	SendErr                                                       string
	Outbox                                                        []*mobilePendingSend
	sending                                                       *mobilePendingSend
	nextSendID                                                    int
	composerEpoch                                                 uint64
	pendingProjection                                             *mobileSendProjection
	readPending                                                   map[string]uint64
	CompletionRead, CompletionReceived                            notification.DesktopRead
	BadgeRevision                                                 uint64
}

func New() *Client {
	m := &Client{Path: "/", Viewer: NewViewer(), ThreadListExpanded: map[string]bool{}}
	m.Viewer.Changed = m.change
	return m
}
func (m *Client) change() {
	if m.Changed != nil {
		m.Changed()
	}
}
func (m *Client) post(f func()) {
	if m.Dispatch != nil {
		m.Dispatch(func() { f(); m.change() })
	} else {
		f()
		m.change()
	}
}
func (m *Client) onMain(gen int, f func()) {
	m.post(func() {
		if gen == m.Generation {
			f()
		}
	})
}
func (m *Client) ComposerEpoch() uint64 { return m.composerEpoch }

func (m *Client) CanSend() bool { return m.Conn != nil || m.SendRequest != nil }
func (m *Client) write(conn *remote.Conn, r remote.Request) error {
	if m.SendRequest != nil {
		return m.SendRequest(r)
	}
	if conn == nil {
		return errors.New(tr("Not connected to desktop"))
	}
	return conn.Send(r)
}

// LoadSaved runs off the UI goroutine. Missing credentials and unavailable
// secure storage are distinct: an unavailable store never destroys pairing.
func (m *Client) LoadSaved(notFound error) {
	store := m.Store
	if store == nil {
		m.post(func() { m.Loaded = true })
		return
	}
	secret, err := store.Get("tunnel-secret")
	if errors.Is(err, notFound) || err == nil && len(secret) != 32 {
		secret = make([]byte, 32)
		if _, err = rand.Read(secret); err == nil {
			err = store.Set("tunnel-secret", secret)
		}
	}
	if err == nil {
		remote.SetClientSecret(secret)
		if m.DataDir != "" {
			remote.SetKeySlotFile(filepath.Join(m.DataDir, "client-key-slot"))
		}
	}
	// The original phone app keeps the active pairing in its secure registry.
	if err != nil {
		ephemeral := make([]byte, 32)
		if _, randomErr := rand.Read(ephemeral); randomErr == nil {
			remote.SetClientSecret(ephemeral)
		}
	}
	link, linkErr := store.Get("nsgo-desktop-link")
	if errors.Is(linkErr, notFound) {
		link, linkErr = store.Get("desktop-link")
		if data, registryErr := store.Get(phonecache.DesktopsKey); registryErr == nil {
			var registry struct {
				Version int                  `json:"version"`
				Active  string               `json:"active"`
				Items   []phonecache.Desktop `json:"items"`
			}
			if json.Unmarshal(data, &registry) == nil && registry.Version == 1 {
				link = nil
				linkErr = nil
				for _, item := range registry.Items {
					if item.ID == registry.Active {
						link = []byte(item.Link)
						break
					}
				}
			}
		}
	}
	m.post(func() {
		m.Loaded = true
		if err != nil {
			m.PersistenceError = tr("Secure pairing storage is unavailable. Pair again after restarting.")
			return
		}
		if linkErr == nil && len(link) > 0 && m.Link == "" {
			m.Connect(string(link))
		}
	})
}

func (m *Client) Connect(raw string) {
	if _, err := remote.ParseLink(raw); err != nil {
		m.Error = err.Error()
		return
	}
	changed := m.Link != raw
	m.PersistState()
	m.FlushCache(true)
	m.Disconnect()
	if changed {
		m.State = phoneproto.State{}
		m.Host = ""
		m.Home()
	}
	m.Link, m.Error, m.Status = raw, "", tr("Connecting to desktop…")
	m.OpenDiskCache(raw)
	m.loadDesktopOptions()
	m.PreviewPaths, m.PreviewRetries, m.Asked = nil, nil, nil
	m.persistPair(raw)

	ctx, cancel := context.WithCancel(context.Background())
	m.Cancel = cancel
	m.Wake = make(chan struct{}, 1)
	go m.Run(ctx, m.Generation, raw, m.Wake)
}
func (m *Client) Disconnect() {
	m.Generation++
	if m.Cancel != nil {
		m.Cancel()
	}
	if m.Conn != nil {
		m.Conn.Close()
	}
	m.Conn, m.Cancel, m.Wake = nil, nil, nil
	m.Connected = false
	m.readPending = nil
	m.RetryAt, m.RetryError = time.Time{}, ""
	m.clearFileReferences()
	if m.Sending {
		m.SendErr = tr("Connection lost. The message may not have been sent.")
		m.failPendingSend()
	}
}
func (m *Client) Forget() {
	m.PersistState()
	m.FlushCache(true)
	m.Disconnect()
	m.Link, m.Host, m.Status = "", "", ""
	m.State = phoneproto.State{}
	m.DropDiskCache()
	m.Home()
	m.persistPair("")
}
func (m *Client) EnterBackground() {
	m.background = true
	m.PersistState()
	m.FlushCache(true)
	m.Disconnect()
}
func (m *Client) EnterForeground() {
	m.background = false
	if m.Link != "" && m.Cancel == nil {
		m.Connect(m.Link)
	}
	m.markCompletionRead()
	m.markOpenRead()
}
func (m *Client) Close() { m.PersistState(); m.FlushCache(true); m.Disconnect() }

func (m *Client) Home() {
	m.Path = "/"
	m.Open, m.NewProject = 0, ""
	m.Viewer.SetActive(0)
	m.resetComposer()
	m.Subscribe(0)
}
func (m *Client) resetComposer() {
	m.composerEpoch++
	m.clearImages()
	m.Draft, m.SendErr = "", ""
	m.clearFileReferences()
}
func (m *Client) OpenThread(t model.Thread) {
	_, p := m.find(t.ID)
	if p.Path != "" {
		m.Selected, m.ExpandedProject = p.Path, p.Path
	}
	cur := m.Viewer.Current()
	if m.Open == t.ID && cur.ServerID == t.ServerID && model.ThreadAgent(cur) == model.ThreadAgent(t) && m.Path == "/thread" {
		return
	}
	m.Open, m.Title, m.NewProject, m.Path = t.ID, t.Title, "", "/thread"
	m.resetComposer()
	if cached := m.CachedMessages(t.ServerID); len(cached) > 0 {
		t.Messages, t.HistoryLoaded = cached, true
	} else {
		t.Loading = true
	}
	m.ShowThread(t)
	m.Subscribe(t.ID)
	m.markOpenRead()
}
func (m *Client) ApplyPhoneState(s phoneproto.State) {
	cur := m.Viewer.Current()
	if cur.ServerID == "" && m.Open != 0 {
		cur, _ = m.find(m.Open)
	}
	old := m.State.Notifications
	m.State = s
	m.State.Notifications = old
	m.ApplyNotifications(s.Notifications)
	if m.Open != 0 && cur.ServerID != "" {
		for _, p := range s.Projects {
			for _, t := range p.Threads {
				if t.ServerID == cur.ServerID && model.ThreadAgent(t) == model.ThreadAgent(cur) && t.ID != m.Open {
					m.Open, cur.ID = t.ID, t.ID
					m.ShowThread(cur)
					m.Subscribe(t.ID)
				}
			}
		}
	}
	m.markOpenRead()
	m.PersistState()
}
func (m *Client) Send()                         { m.sendDraft(false) }
func (m *Client) Steer()                        { m.sendDraft(true) }
func (m *Client) Stop()                         { m.stop() }
func (m *Client) QueueAction(op string, id int) { m.queueAction(op, id) }
func (m *Client) Action(r remote.Request)       { m.sendPhoneAction(r) }
func (m *Client) Handoff()                      { m.handoffToNewThread() }
func (m *Client) Agent() string {
	if m.Open != 0 {
		return model.ThreadAgent(m.Viewer.Current())
	}
	return m.composerAgent()
}
func (m *Client) composerAgent() string {
	if m.NewAgent != "" {
		return m.NewAgent
	}
	if m.State.Agent != "" {
		return m.State.Agent
	}
	return m.defaultAgent()
}
func (m *Client) SelectedProject() model.ProjectSnapshot            { return m.selectedProject() }
func (m *Client) SetOptions(agent string, o remote.SendOptions)     { m.setOptions(agent, o) }
func (m *Client) AgentOptions(agent string) phoneproto.AgentOptions { return m.agentOptions(agent) }
func (m *Client) CompletionUnread(t model.Thread) bool              { return m.completionUnread(t) }
func (m *Client) MarkAllRead()                                      { m.markAllRead() }
func (m *Client) OpenReference(raw string)                          { m.openCollaborationThread(raw) }
func (m *Client) FileTarget(rel string) (string, string, bool)      { return m.messageFileTarget(rel) }
func (m *Client) RetryNow() bool                                    { return m.retryNow() }
func (m *Client) ReferenceThread(ref model.ThreadRef) (model.Thread, model.ProjectSnapshot, bool) {
	return m.referencedThread(ref)
}

// Revision checks serialize quick pairing/forget actions without letting an
// older worker overwrite a newer credential. Links never reach ordinary files.
func (m *Client) persistPair(link string) {
	if m.Store == nil {
		return
	}
	store := m.Store
	revision := m.storeRevision.Add(1)
	go func() {
		m.storeMu.Lock()
		defer m.storeMu.Unlock()
		if revision != m.storeRevision.Load() {
			return
		}
		err := store.Set("nsgo-desktop-link", []byte(link))
		if err != nil {
			m.post(func() {
				if revision == m.storeRevision.Load() {
					m.PersistenceError = tr("Secure pairing storage is unavailable. Pair again after restarting.")
				}
			})
		}
	}()
}
