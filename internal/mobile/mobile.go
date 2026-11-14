package mobile

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/imagepicker"
	"github.com/egoist/mygo/plugins/scanner"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	conversation "miya/internal/chat/view"
	"miya/internal/desktop/control"
	"miya/internal/interaction"
	"miya/internal/memory/manager"
	"miya/internal/notification"
	phonecache "miya/internal/phone/cache"
	phoneproto "miya/internal/phone/protocol"
	phoneproxy "miya/internal/phone/proxy"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/prefs"
	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
	"miya/internal/textutil"
	"miya/internal/thumbnail"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// The phone shows one desktop's projects and conversations, sends messages
// into them and answers their approvals. The desktop's background service
// runs the agents; the phone holds only what the desktop sent it.
type App struct {
	DesktopPreviews         map[string]*MobileWindowPreview
	DesktopRequest          string
	desktopOpening          remote.Request
	desktopOpenTimer        *time.Timer
	background              bool
	DesktopApproval         MobileDesktopApproval
	ConversationMenu        bool
	conversationMenuProject string
	conversationMenuAgent   string
	filesProxy              *phoneproxy.PhoneFileProxy
	FilesCancel             context.CancelFunc
	DesktopPage             *MobileDesktopPage
	desktopSend             func(*remote.Conn, remote.Request) error
	desktopClient           func(*remote.Conn, string) *control.Client
	// desktopClosing are leases closed on this connection whose desktop-close
	// a reopen sends first, so the Mac revokes them before the new open.
	DesktopClosing               []string
	WebPort                      string
	WebRef                       model.ThreadRef
	WebDetails                   bool
	WebProject                   string
	WebManual                    bool
	WebServices                  *mobileWebServices
	FileRefs                     *MobileFileReferences
	approvalID                   json.RawMessage
	Options                      map[string]remote.SendOptions
	optionsAgent                 string
	ApprovalPending              map[string]bool
	ApprovalFields               map[string]map[string]string
	readPending                  map[string]uint64
	CompletionRead               appmodel.DesktopRead
	CompletionReceived           appmodel.DesktopRead
	BadgeRevision                uint64
	badgeApplied                 int
	pushOpenRef                  remote.Request
	pushID, pushToken, pushError string
	PushDisabled, pushRequesting bool
	// pushLive: Android receives notifications over the paired connection,
	// kept open in the background by keepRunning (mygo.Power.KeepRunning).
	pushLive            bool
	keepRunning         func()
	liveShown           map[string]*mygo.Notification
	pushOpen            int
	win                 *mygo.Window
	DesktopAuthenticate func(context.Context) error
	Store               phonecache.SecretStore
	Desktops            MobileDesktops
	desktopSessions     map[string]phonecache.Session
	DesktopWrites       desktopPersistence
	desktopError        string
	renamingDesktop     string
	desktopNameDraft    string
	DesktopNotice       string
	desktopLoadFailed   bool
	DialDesktop         func(context.Context, string) (*remote.Conn, error)
	// view renders a conversation with the desktop's own message views, over
	// a controller that only holds what the desktop sent.
	View *conversation.View
	// composerH is the height of the composer floating over the messages,
	// in the previous frame.
	composerH  float32
	Viewer     *mobileViewer
	FormErrors map[string]string
	Nav        *ui.Router
	Pal        mobilePalette
	Dark       bool
	// themed reports pal and the theme set for the current appearance.
	themed            bool
	textScale         float32
	instantNavigation bool

	Loaded             bool
	Link, Host         string
	State              phoneproto.State
	Memories           MobileMemory
	Autos              mobileAutomations
	Collaborations     widgets.CollaborationUI
	CollaborationCalls map[string]*MobileCollaborationCall
	// selected is the phone's workspace for new conversations.
	Selected           string
	ExpandedProject    string
	projectsExpanded   bool
	ThreadListExpanded map[string]bool
	DrawerSearch       bool
	DrawerQuery        string
	Menu               bool
	QuotaOpen          bool
	quotaAgent         string
	quotaDrawerShown   bool
	// drawerOffset is where the list page's row rests or is dragged to:
	// 0 closed, drawerW open.
	DrawerOffset   ui.Point
	DrawerW        float32
	DrawerDragging bool
	// drawerVisible makes a tap on the list beside the drawer close it.
	DrawerVisible bool
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
	Open            int
	Title           string
	NewProject      string
	NewAgent        string
	Draft           string
	InlineInput     conversation.InlineThreadInput
	voice           *mobileVoice
	voiceStart      func(context.Context) (mobileVoiceSession, error)
	voicePermission string
	VoiceAutoSend   bool
	pairInput       mobileTextField
	// images are photos to attach to the next message, as cache copies.
	Images []string
	// previewPaths maps desktop image sources to phone-local thumbnails.
	// An empty value reserves an in-flight lookup or download.
	PreviewPaths   map[string]string
	PreviewRetries map[string]int
	// commands are Claude Code\'s slash commands, by project; asked
	// marks those requested on this connection.
	Commands map[string][]model.ComposerChoice
	// msgCache keeps conversations\' messages by ServerID, most recent
	// last in msgKeys, so reopening one sends only what changed.
	MsgCache map[string][]model.Message
	msgKeys  []string
	// dataDir is the app\'s user data; cacheDir the current desktop\'s
	// conversations on disk, and dirty what is yet to be written there.
	DataDir, CacheDir string
	cacheWork         mobileCacheWork
	Dirty             map[string]any
	flushTimer        *time.Timer
	Asked             map[string]bool
	Sending           bool
	Outbox            []*mobilePendingSend
	sending           *mobilePendingSend
	nextSendID        int
	composerEpoch     uint64
	pendingProjection *mobileSendProjection
	SendErr           string

	Conn       *remote.Conn
	Cancel     context.CancelFunc
	Generation int
	Connected  bool
	Status     string
	RetryAt    time.Time
	RetryError string
	// wake cuts the current connection's reconnect wait short; nil when
	// no connection is running.
	Wake     chan struct{}
	net      mobileNetworkPath
	err      string
	scanning bool
	Paste    string
}

var ErrIncompatible = errors.New(tr("Incompatible desktop version. Update both the mobile and desktop apps."))

// mobilePalette holds the phone's colors for one appearance.
type mobilePalette struct {
	Bg, surface, ink, muted, line, bubble, ok, warn ui.Color
	// link colors links in messages, as the accent is ink.
	link ui.Color
	// drawer stands out from the page behind its scrim.
	Drawer ui.Color
}

var LightPalette = mobilePalette{
	Bg: ui.Hex("#ffffff"), surface: ui.Hex("#f2f2f4"), ink: ui.Hex("#141414"), muted: ui.Hex("#8a8a8f"),
	line: ui.Hex("#e3e3e6"), bubble: ui.Hex("#f2f2f4"), ok: ui.Hex("#30a46c"), warn: ui.Hex("#c27c0e"), Drawer: ui.Hex("#ffffff"),
	link: ui.Hex("#2563eb"),
}

var DarkPalette = mobilePalette{
	Bg: ui.Hex("#000000"), surface: ui.Hex("#1c1c1e"), ink: ui.Hex("#f2f2f2"), muted: ui.Hex("#8e8e93"),
	line: ui.Hex("#2c2c2e"), bubble: ui.Hex("#1c1c1e"), ok: ui.Hex("#3dd68c"), warn: ui.Hex("#f5a524"), Drawer: ui.Hex("#000000"),
	link: ui.Hex("#6ba4ff"),
}

// applyPalette follows the system appearance. The phone runs only this
// window, so the shared colors the message views read are set here too.
func (m *App) ApplyPalette(dark bool) {
	p, t := LightPalette, ui.LightTheme()
	if dark {
		p, t = DarkPalette, ui.DarkTheme()
	}
	m.Pal, m.Dark, m.themed = p, dark, true
	widgets.Canvas, widgets.Side, widgets.Panel, widgets.Ink, widgets.Muted, widgets.Line = p.Bg, p.Bg, p.Bg, p.ink, p.muted, p.line
	widgets.Hover, widgets.Chosen, widgets.Bubble = p.surface, p.surface, p.bubble
	t.Background, t.Surface, t.SurfaceHover, t.Border = p.Bg, p.surface, p.line, p.line
	t.Text, t.TextMuted, t.Accent, t.AccentText = p.ink, p.muted, p.ink, p.Bg
	t.FontSize = m.Font(17)
	m.View.Theme = t
	m.View.Md.Theme = &markdown.Theme{Link: p.link}
}

func NewMobileApp() *App {
	m := &App{badgeApplied: -1, VoiceAutoSend: prefs.Load(voiceAutoSendPreference) != "off"}
	m.Viewer = newMobileViewer()
	m.View = conversation.NewConversationView(m.Viewer)
	m.View.Mobile = true
	m.FormErrors = map[string]string{}
	m.View.Md = markdown.Options{
		OnLink:    m.openMessageLink,
		LoadImage: func(src string) (ui.ImageSource, error) { return widgets.LoadImage(widgets.ImageSourcePath(src)) },
	}
	m.View.ImagePreview = m.MessageImage
	m.View.OnHandoff = m.handoffToNewThread
	m.View.WorkingAccessory = m.collaborationToggle
	m.View.OnRetry = func(req appmodel.RetryRequest) {
		r := m.ThreadRequest("retry", req.ThreadID)
		r.MessageID = req.MessageID
		m.sendPhoneAction(r)
	}
	m.View.ReferenceThread = func(ref model.ThreadRef) (model.Thread, bool) {
		t, _, ok := m.referencedThread(ref)
		return t, ok
	}

	return m
}

func mobileMain() {
	widgets.ConfigureInterfaceLanguage()
	m := NewMobileApp()
	m.Store = mygo.Secrets
	m.PushDisabled = prefs.Load("phone-notifications") == "off"
	mygo.App.OnOpenURL(func(raw string) {
		uiruntime.RunOnMain(func() {
			if _, err := remote.ParseLink(raw); err == nil {
				m.Connect(raw)
			}
		})
	})
	mygo.App.WhenReady(func() {
		m.win = mygo.NewWindow(mygo.WindowOptions{Title: "Miya", Width: 390, Height: 844, ExtendIntoSafeArea: true, Content: ui.View(m.ViewRoot)})
		uiruntime.SetAppWindow(m.win)
		m.watchNetwork()
		m.setupPush()
		go m.loadSaved()

	})
	// iOS ends a suspended app's sockets; reconnect on return instead.
	// Write the conversations before iOS suspends the app, which may end it.
	mygo.App.OnDidEnterBackground(func() {
		uiruntime.RunOnMain(func() {
			m.enterBackground(runtime.GOOS == "android")
		})
	})
	mygo.App.OnDidBecomeActive(func() {
		uiruntime.RunOnMain(func() {
			m.requestPush()
			m.markCompletionRead()
		})
	})
	mygo.App.OnWillEnterForeground(func() {
		uiruntime.RunOnMain(func() {
			m.enterForeground()
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// loadSaved reads the tunnel secret and the paired desktop off the main
// thread, as Keychain reads may block.
func (m *App) loadSaved() {
	var pushID string
	dataDir, _ := mygo.App.Path(mygo.PathUserData)
	if m.Store != nil {
		if saved, err := m.Store.Get("notification-device"); err == nil {
			pushID = string(saved)
		}
		if len(pushID) != 32 {
			var data [16]byte
			if _, err := rand.Read(data[:]); err == nil {
				pushID = fmt.Sprintf("%x", data[:])
				_ = m.Store.Set("notification-device", []byte(pushID))
			}
		}
		secret, err := m.Store.Get("tunnel-secret")
		if errors.Is(err, mygo.ErrSecretNotFound) || err == nil && len(secret) != 32 {
			secret = make([]byte, 32)
			rand.Read(secret)
			err = m.Store.Set("tunnel-secret", secret)
		}
		if err == nil {
			remote.SetClientSecret(secret)
			if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
				remote.SetKeySlotFile(filepath.Join(dir, "client-key-slot"))
			}
		}
	}
	saved, migrated, desktopErr := ReadMobileDesktops(m.Store)
	if migrated {
		if item, ok := saved.find(saved.Active); ok {
			phonecache.MigrateOptions(dataDir, item.Link)
		}
	}
	uiruntime.RunOnMain(func() {
		m.Loaded = true
		m.pushID = pushID
		m.DataDir = dataDir
		m.desktopLoadFailed = desktopErr != nil
		if desktopErr != nil {
			m.desktopError, m.err = desktopErr.Error(), desktopErr.Error()
			return
		}
		m.desktopError = ""
		m.restoreDesktops(saved)
		if item, ok := m.Desktops.find(m.Desktops.Active); ok && m.Link == "" {
			m.Connect(item.Link)
		}
		m.SaveDesktops()
		m.requestPush()
	})
}

// onMain runs f on the main thread unless a newer connection replaced gen.
func (m *App) onMain(gen int, f func()) {
	uiruntime.RunOnMain(func() {
		if m.Generation == gen {
			f()
		}
	})
}

func (m *App) Connect(link string) {
	if m.desktopLoadFailed {
		m.err = tr("Reload the saved desktop list first")
		return
	}
	item, err := m.Desktops.Remember(link, "")
	if err != nil {
		m.err = err.Error()
		return
	}
	link = item.Link
	changed := link != m.Link
	if changed && m.Link != "" {
		m.saveDesktopSession()
		m.PersistState()
		m.FlushCache(true)
	}
	m.readPending = nil
	m.ApprovalPending = nil
	m.disconnectDesktop(false, !changed && m.DesktopPage != nil && m.DesktopPage.suspended)
	m.PreviewPaths = nil
	m.PreviewRetries = nil
	if changed {
		m.ResetDesktopState()
	}
	m.Desktops.Active = item.ID
	m.Link, m.err, m.Status = link, "", tr("Connecting to desktop…")
	m.OpenDiskCache(link)
	if m.Host != "" {
		_, _ = m.Desktops.Remember(link, m.Host)
	}
	if m.Host == "" {
		m.Host = item.Name
	}
	if changed {
		m.loadDesktopOptions()
		m.restoreDesktopSession(item.ID)
	}
	m.updateBadge()
	m.SaveDesktops()
	m.Asked = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.Cancel = cancel
	m.Wake = make(chan struct{}, 1)
	go m.Run(ctx, m.Generation, link, m.Wake)
}

// Background releases live control and its sensitive state, retaining only
// the target needed to reopen the view when the paired connection returns.
func (m *App) enterBackground(preserveBrowser bool) {
	m.background = true
	m.cancelVoice()
	m.FlushCache(true)
	m.suspendDesktopPage()
	// Android keeps the connection while a foreground service keeps the
	// process running: notifications arrive over it.
	if preserveBrowser && (m.FilesCancel != nil || m.keepRunning != nil) {
		return
	}
	m.disconnectDesktop(false, true)
}

func (m *App) enterForeground() {
	m.background = false
	m.updateKeepRunning()
	if m.Link != "" && m.Cancel == nil {
		m.Connect(m.Link)
	} else {
		m.resumeDesktopPage()
	}
}

func (m *App) Disconnect() {
	m.DisconnectDesktop(false)
}

func (m *App) DisconnectDesktop(unregister bool) {
	m.disconnectDesktop(unregister, false)
}

func (m *App) disconnectDesktop(unregister, preserveDesktop bool) {
	defer m.updateKeepRunning()
	m.cancelVoice()
	if !preserveDesktop {
		m.DesktopApproval = MobileDesktopApproval{}
	}
	m.interruptAutomationRequest()
	m.clearFileReferences()
	m.clearWebServices()
	if !m.RetryAt.IsZero() {
		m.Status = ""
	}
	m.RetryAt, m.RetryError = time.Time{}, ""
	if m.FilesCancel != nil {
		m.FilesCancel()
		m.FilesCancel = nil
	}
	if m.DesktopPage != nil && !preserveDesktop {
		m.DesktopFinish(m.DesktopPage, tr("Disconnected from your Mac. Return to the conversation to reopen."))
	}
	if m.filesProxy != nil {
		m.filesProxy.Close()
		m.filesProxy = nil
	}
	m.Generation++
	conn, cancel := m.Conn, m.Cancel
	m.Cancel, m.Wake = nil, nil
	if unregister && conn != nil {
		registration, _ := json.Marshal(notification.Registration{ID: m.pushID, Token: m.pushToken, Disabled: true})
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
	m.stopDesktopOpen()
	m.Conn, m.Connected = nil, false
	m.DesktopClosing = nil // leases end with their connection
	m.DesktopPreviews = nil
	m.Autos.Loading = false
	if m.Sending {
		m.Sending, m.SendErr = false, tr("Connection lost. The message may not have been sent.")
		m.failPendingSend()
	}
}

// forget unpairs the desktop.
func (m *App) Forget() {
	m.RemoveDesktop(m.CurrentDesktopID())
}

func (m *App) Run(ctx context.Context, gen int, link string, wake <-chan struct{}) {
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			m.onMain(gen, func() {
				m.RetryAt, m.RetryError = time.Time{}, ""
				m.Status = tr("Reconnecting to desktop…")
			})
		}
		dial := m.DialDesktop
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
		if errors.Is(err, ErrIncompatible) {
			delay = 15 * time.Second
		}
		message := fmt.Sprintf(tr("Disconnected. Retrying in %d seconds."), int(delay.Seconds()))
		if err != nil {
			message += ": " + err.Error()
		}
		retryAt := time.Now().Add(delay)
		retryError := ""
		if err != nil {
			retryError = err.Error()
		}
		m.onMain(gen, func() {
			m.suspendDesktopPage()
			m.stopDesktopOpen()
			m.Conn, m.Connected, m.Status = nil, false, message
			m.DesktopPreviews = nil
			m.interruptAutomationRequest()
			m.RetryAt, m.RetryError = retryAt, retryError
			if m.filesProxy != nil {
				m.filesProxy.SetConn(nil)
			}
			if m.Sending {
				m.Sending, m.SendErr = false, tr("Connection lost. The message may not have been sent.")
				m.failPendingSend()
			}
		})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-wake:
			// Someone needs the desktop now: a send, or a network that came back.
			timer.Stop()
		}
	}
}

// read delivers the desktop's frames until the connection ends. greeted
// reports whether the desktop answered, so a dropped link retries promptly.
func (m *App) read(gen int, conn *remote.Conn) (greeted bool, err error) {
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
				return greeted, ErrIncompatible
			}
			greeted = true
			if err := conn.EnableAttachments(f.AttachmentToken); err != nil {
				return greeted, err
			}
			// The desktop compresses everything after hello.
			conn.DecompressReceives()
			host := f.Host
			m.onMain(gen, func() {
				m.Host, m.Connected, m.Status, m.err = host, true, "", ""
				m.RetryAt, m.RetryError = time.Time{}, ""
				_, _ = m.Desktops.Remember(m.Link, host)
				m.Conn = conn
				m.CompletionRead = appmodel.DesktopRead{}
				m.CompletionReceived = appmodel.DesktopRead{}
				m.Autos.Loading, m.Autos.RequestedAt = false, time.Time{}
				m.Memories.loading, m.Memories.RequestedAt = false, time.Time{}
				if m.filesProxy != nil {
					m.filesProxy.SetConn(conn)
				}
				m.PreviewPaths = nil // Retry previews requested while offline.
				m.PreviewRetries = nil
				if open := m.Open; open != 0 {
					r := m.SubscribeRequest(open)
					go conn.Send(r)
				}
				m.BadgeRevision = 0
				m.syncPush()
				m.SaveDesktops()
			})
		case "desktop-open":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.receiveDesktopControl(conn, data, message) })
		case "desktop-preview":
			var p control.Preview
			if json.Unmarshal(f.Data, &p) == nil {
				var image ui.ImageSource
				message := f.Error
				if len(p.Data) > 0 && len(p.Data) <= thumbnail.MaxBytes {
					image, _ = ui.DecodeImage(p.Data)
					if image == nil {
						message = tr("Unable to decode window preview")
					}
				}
				m.onMain(gen, func() { m.ReceiveWindowPreview(conn, p, image, message) })
			}
		case "desktop-action":
			var p control.Preview
			if json.Unmarshal(f.Data, &p) == nil {
				message := f.Error
				m.onMain(gen, func() { m.receiveWindowAction(conn, p, message) })
			}
		case "memory":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.ReceiveMemory(data, message) })
		case "automation-webhook":
			data, message := f.Data, f.Error
			m.onMain(gen, func() {
				if message != "" {
					return
				}
				var v automation.WebhookView
				if json.Unmarshal(data, &v) == nil {
					m.Autos.Webhook = &v
				}
			})
		case "automations":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.ReceiveAutomations(data, message) })
		case "collaboration":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.ReceiveCollaboration(data, message) })
		case "automation-run":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.ReceiveAutomationRun(data, message) })
		case "automation-draft", "automation-create":
			op, data, message := f.Type, f.Data, f.Error
			m.onMain(gen, func() { m.ReceiveAutomationForm(op, data, message) })
		case "action":
			message := f.Error
			m.onMain(gen, func() {
				if message != "" {
					m.SendErr = message
				}
			})
		case "responded":
			var result phoneproto.ApprovalReply
			if json.Unmarshal(f.Data, &result) == nil {
				message := f.Error
				m.onMain(gen, func() { m.ApprovalAnswered(result.ID, message) })
			}
		case "read":
			var n notification.PhoneState
			if json.Unmarshal(f.Data, &n) == nil {
				m.onMain(gen, func() { m.ApplyNotifications(n) })
			}
		case "notice":
			var n notification.LiveMessage
			if json.Unmarshal(f.Data, &n) == nil {
				m.onMain(gen, func() { m.showLiveNotice(n) })
			}
		case "push":
			message := f.Error
			m.onMain(gen, func() { m.pushError = message; m.readPending = nil; m.markOpenRead() })
		case "state":
			var s phoneproto.State
			if json.Unmarshal(f.Data, &s) == nil {
				m.onMain(gen, func() {
					m.ApplyPhoneState(s)
				})
			}
		case "thread":
			var d phoneproto.ThreadDelta
			if json.Unmarshal(f.Data, &d) == nil {
				id := f.Thread
				m.onMain(gen, func() {
					m.ReceiveThread(id, d)
				})
			}
		case "image":
			var preview thumbnail.Image
			if json.Unmarshal(f.Data, &preview) == nil {
				if f.Error != "" {
					m.onMain(gen, func() { m.RetryImage(preview.Source, conn) })
					continue
				}
				if f.Attachment != "" {
					id := f.Attachment
					go func() {
						data, err := conn.ReadAttachment(conn.Context(), id)
						if err != nil {
							m.onMain(gen, func() { m.RetryImage(preview.Source, conn) })
							return
						}
						preview.Data = data
						m.onMain(gen, func() {
							if m.Conn == conn {
								m.ReceiveImage(preview)
							}
						})
					}()
				} else {
					m.onMain(gen, func() { m.ReceiveImage(preview) })
				}
			}
		case "commands":
			var pc phoneproto.Commands
			if json.Unmarshal(f.Data, &pc) == nil {
				m.onMain(gen, func() {
					if m.Commands == nil {
						m.Commands = map[string][]model.ComposerChoice{}
					}
					if pc.Commands == nil {
						pc.Commands = []model.ComposerChoice{}
					}
					if pc.Agent == "" {
						pc.Agent = agentmeta.LegacyCommandAgent()
					}
					m.Commands[agent.CommandKey(pc.Agent, pc.Project)] = pc.Commands
				})
			}
		case "sent":
			id, msg := f.Thread, f.Error
			m.onMain(gen, func() { m.Sent(id, msg) })
		}
	}
}

// The desktop may renumber threads after a restart or while repairing an old
// cache. Rebind the open page by durable identity before handling its deltas.
func (m *App) ApplyPhoneState(s phoneproto.State) {
	m.CloseResolvedDesktopControl(s.Approvals)
	if m.State.MemoryRevision != s.MemoryRevision {
		m.Memories.List = manager.List{}
		m.Memories.RequestedAt = time.Time{}
	}
	for _, old := range m.State.Approvals {
		same := false
		for _, next := range s.Approvals {
			if textutil.SameJSON(old.ID, next.ID) && phoneproto.ApprovalFingerprint(old) == phoneproto.ApprovalFingerprint(next) {
				same = true
				break
			}
		}
		if !same {
			id := string(old.ID)
			delete(m.ApprovalFields, id)
			delete(m.ApprovalPending, id)
			delete(m.DesktopPreviews, id)
			delete(m.FormErrors, id)
		}
	}
	cur := m.Viewer.Current()
	if cur.ServerID == "" && m.Open != 0 {
		cur, _ = m.find(m.Open)
	}
	notifications := m.State.Notifications
	m.State = s
	m.State.Notifications = notifications
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
	m.openPushThread()
	m.markOpenRead()
	for id := range m.ApprovalFields {
		live := false
		for _, a := range s.Approvals {
			if string(a.ID) == id {
				live = true
				break
			}
		}
		if !live {
			delete(m.ApprovalFields, id)
			delete(m.ApprovalPending, id)
			delete(m.FormErrors, id)
		}
	}
	m.Viewer.setApprovals(s.Approvals)
	m.PersistState()
	m.resumeDesktopPage()
}

func (m *App) ReceiveThread(id int, d phoneproto.ThreadDelta) {
	if m.Open != id {
		return
	}
	cur := m.Viewer.Current()
	if cur.ID == id && cur.ServerID != "" {
		if d.Thread.ServerID == "" || (d.Thread.ServerID == cur.ServerID && (d.Thread.Loading || !d.Thread.HistoryLoaded) && len(d.Order) == 0) {
			// A loading or missing-thread response is not an empty history.
			// Keep the displayed messages and cache until history arrives,
			// including responses from desktops that still send an empty order.
			cur.Status, cur.Loading = d.Thread.Status, d.Thread.Loading
			m.updateThreadView(cur, false)
			return
		}
		if d.Thread.ServerID != cur.ServerID || model.ThreadAgent(d.Thread) != model.ThreadAgent(cur) {
			return
		}
	}
	d.Thread.ID = id
	m.ApplyThread(d)
	m.receivePendingSends(m.Viewer.Current())
	// Only a live delta confirms that the phone has the completed contents;
	// sidebar metadata combined with cached messages does not.
	if appmodel.SameMessageOrder(m.Viewer.Current().Messages, d.Order) {
		m.CompletionReceived = m.completionVersion(d.Thread)
		m.markCompletionRead()
	}
}

func (m *App) ShowThread(t model.Thread) {
	m.updateThreadView(t, true)
}

func (m *App) updateThreadView(t model.Thread, messagesChanged bool) {
	v := m.Viewer
	v.SetThreads([]model.Thread{t})
	v.SetActive(t.ID)
	if messagesChanged {
		v.setRevision(v.Revision() + 1)
	}
	if t.Title != "" {
		m.Title = t.Title
	}
}

func (m *App) Subscribe(id int) {
	m.CompletionRead = appmodel.DesktopRead{}
	m.CompletionReceived = appmodel.DesktopRead{}
	if conn := m.Conn; conn != nil {
		r := m.SubscribeRequest(id)
		go conn.Send(r)
	}
}

// find looks a conversation up in the desktop's state.
func (m *App) find(id int) (model.Thread, model.ProjectSnapshot) {
	for _, p := range m.allPhoneProjects() {
		for _, t := range p.Threads {
			if t.ID == id {
				return t, p
			}
		}
	}
	return model.Thread{}, model.ProjectSnapshot{}
}

func (m *App) project(path string) model.ProjectSnapshot {
	for _, p := range m.allPhoneProjects() {
		if p.Path == path {
			return p
		}
	}
	return model.ProjectSnapshot{Path: path, Name: filepath.Base(path)}
}

// currentProject is the desktop's open project, where a new conversation
// starts.
func (m *App) currentProject() model.ProjectSnapshot {
	for _, p := range m.State.Projects {
		if p.Current {
			return p
		}
	}
	if len(m.State.Projects) > 0 {
		return m.State.Projects[0]
	}
	return model.ProjectSnapshot{}
}

func (m *App) ApprovalsFor(serverID string) []interaction.Request {
	if serverID == "" {
		return nil
	}
	var out []interaction.Request
	for _, a := range m.State.Approvals {
		if a.ThreadID == serverID {
			out = append(out, a)
		}
	}
	return out
}

func (m *App) resetComposer() {
	m.cancelVoice()
	m.composerEpoch++
	m.clearImages()
	m.Draft, m.SendErr = "", ""
}

func (m *App) OpenThread(t model.Thread) {
	m.Collaborations.ClosePopovers()
	m.DrawerSearch, m.DrawerQuery = false, ""
	_, project := m.find(t.ID)
	if t.ServerID != "" {
		_, project, _ = m.referencedThread(model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID})
	}
	if project.Path != "" {
		m.Selected, m.ExpandedProject, m.projectsExpanded = project.Path, project.Path, true
	}
	cur := m.Viewer.Current()
	if m.Open == t.ID && cur.ServerID == t.ServerID && model.ThreadAgent(cur) == model.ThreadAgent(t) && m.Router().Path() == "/thread" {
		m.Menu = false
		return
	}
	m.DesktopRequest = ""
	m.Open, m.Title, m.NewProject, m.Menu = t.ID, t.Title, "", false
	m.resetComposer()
	// Show what the phone holds at once; the desktop sends what changed.
	if cached := m.CachedMessages(t.ServerID); len(cached) > 0 {
		t.Messages, t.HistoryLoaded = cached, true
	} else {
		t.Loading = true
	}
	m.ShowThread(t)
	m.Subscribe(t.ID)
	m.OpenDrawerPage("/thread")
	m.markOpenRead()
}

// leave closes the conversation page's state, after the router went back.
func (m *App) leave() {
	m.DesktopRequest = ""
	m.Open, m.NewProject = 0, ""
	m.Viewer.SetActive(0)
	m.Subscribe(0)
	m.resetComposer()
}

// home returns to the new-conversation page at once.
func (m *App) Home() {
	if m.Nav != nil && m.Nav.Path() != "/" {
		// Resetting/replacing a draft reuses one controller; there is no
		// independent outgoing conversation to animate in this case.
		m.instantNavigation = true
	}
	m.Router().Reset("/")
	m.leave()
}

func (m *App) Router() *ui.Router {
	if m.Nav == nil {
		m.Nav = ui.NewRouter("/")
		m.Nav.Transition = ui.TransitionStack
		m.Nav.Animation = &ui.Animation{Duration: 280 * time.Millisecond, Ease: ui.EaseOut}
	}
	return m.Nav
}

func (m *App) Send() { m.sendDraft(false) }

func (m *App) sendDraft(steer bool) {
	if m.Sending || m.voice != nil {
		return
	}
	conn := m.Conn
	if conn == nil {
		m.SendErr = m.disconnectedSendError()
		return
	}
	if model.HasAgentReferences(m.Draft, nil) && (m.State.Collaboration == nil || m.State.Collaboration.Version < 1) {
		m.SendErr = tr("Update the Miya background service to use Agent tags")
		return
	}
	if m.Open == 0 && m.NewProject == "" {
		project := m.selectedProject()
		if project.Path == "" {
			m.SendErr = tr("Add a project on your desktop first")
			return
		}
		m.NewProject, m.NewAgent = project.Path, m.composerAgent()
		m.Router().Push("/thread")
	}
	r := m.ThreadRequest("send", m.Open)
	r.Text = strings.TrimSpace(m.Draft)
	r.Steer = steer
	agent := m.composerAgent()
	if m.Open != 0 {
		agent = model.ThreadAgent(m.Viewer.Current())
	}
	if len(m.State.Agents) > 0 {
		o := m.SendOptions(agent)
		r.Options = &o
	}
	if m.Open == 0 {
		r.Project, r.Agent = m.NewProject, m.NewAgent
	}
	if r.Text == "" && len(m.Images) == 0 {
		return
	}
	m.BeginSend(r, m.Images)
	m.transmitSend(m.sending)
}

func (m *App) stop() {
	if conn, id := m.Conn, m.Open; conn != nil && id != 0 {
		r := m.ThreadRequest("stop", id)
		go conn.Send(r)
	}
}

func (m *App) scan() {
	if m.scanning {
		return
	}
	m.scanning, m.err = true, ""
	go func() {
		raw, err := scanner.Scan(context.Background(), scanner.Options{Prompt: tr("Scan the QR code in Miya on your desktop"), CancelLabel: tr("Cancel")})
		uiruntime.RunOnMain(func() {
			m.scanning = false
			if err != nil {
				m.err = scanError(err)
			} else if raw != "" {
				m.pair(raw)
			}
		})
	}()
}

func (m *App) pair(raw string) {
	if _, err := remote.ParseLink(raw); err != nil {
		m.err = err.Error()
		return
	}
	m.Paste = ""
	m.Connect(raw)
}

// scanError explains a failed scan; closing the scanner is not a failure.
func scanError(err error) string {
	switch {
	case errors.Is(err, scanner.ErrCanceled):
		return ""
	case errors.Is(err, mygo.ErrUnsupported):
		return tr("Scan on your phone, or paste a desktop connection code")
	case errors.Is(err, scanner.ErrCameraDenied):
		return tr("Allow Miya to use the camera in Settings, or paste a connection code")
	case errors.Is(err, scanner.ErrCameraUnavailable):
		return tr("Camera unavailable. Paste a desktop connection code.")
	}
	return tr("Unable to open the camera")
}

func (m *App) ConnectionLine() string {
	switch {
	case m.err != "":
		return m.err
	case m.Status != "":
		return m.Status
	case !m.Connected:
		return tr("Disconnected")
	}
	return ""
}

func (m *App) ViewRoot(c *ui.Context) {
	if m.voice != nil && (m.voice.owner != m.voiceOwner() || !m.voicePage() || m.Menu || m.ConversationMenu) {
		m.cancelVoice()
	}
	widgets.UseInterfaceLanguage(c)
	m.View.Md.LinkMenu = widgets.MessageLinkMenu(c, m.ComposerProject())
	m.updateReconnectCountdown(c)
	m.textScale = c.Preferences().TextScale
	// Before SetTheme, the theme is the system's appearance.
	if dark := c.Theme().Dark; !m.themed || dark != m.Dark {
		m.ApplyPalette(dark)
	}
	m.View.Theme.FontSize = m.Font(17)
	c.SetTheme(m.View.Theme)
	p := m.Pal
	// Honor any safe-area padding supplied by the host.
	safe := safeAreaOf(c)
	pageColor := p.Bg
	contentSafe := safe
	if m.Loaded && m.Link != "" {
		if m.Router().Path() == "/" {
			pageColor = m.HomeBackground()
		} else if mobileSettingsPage(m.Router().Path()) {
			pageColor = m.settingsBackground()
		}
		// Keep the router's bounds fixed throughout navigation. Each page
		// owns its safe-area padding, including the outgoing page, so a
		// history change cannot resize or vertically shift the list.
		contentSafe = safeArea{}
	}
	// MyGo uses the opaque root fill for the surrounding iOS safe area,
	// including the status bar and home indicator.
	c.Root().Background(pageColor)
	ui.Column(c).Fill().Padding(contentSafe.Top, contentSafe.Right, contentSafe.Bottom, contentSafe.Left).Background(pageColor).TextColor(p.ink).FontSize(m.Font(15)).Children(func() {
		switch {
		case !m.Loaded:
			ui.Spinner(c).Margin(ui.Auto)
		case m.Link == "":
			m.pairView(c)
		default:
			// Drawer destinations share the edge gesture; child pages pop.
			// Remote gestures own the desktop page edges; leave with its back button.
			m.Router().InteractiveBack = !mobilePageHasDrawer(m.Router().Path()) && m.Router().Path() != "/desktop"
			transition := m.Router().Transition
			instant := m.instantNavigation
			m.instantNavigation = false
			if instant {
				m.Router().Transition = ui.TransitionNone
			}
			m.Router().View(c, func(page *ui.Route) {
				page.Page().Background(p.Bg).Padding(safe.Top, safe.Right, safe.Bottom, safe.Left)
				if mobileSettingsPage(page.Path()) {
					page.Page().Background(m.settingsBackground())
				}
				if mobilePageHasDrawer(page.Path()) {
					page.Page().Padding(0)
					m.listPage(c, page.Path())
					return
				}
				switch page.Path() {
				case "/settings/language":
					m.languageSettingsPage(c)
				case "/settings/voice":
					m.voiceSettingsPage(c)
				case "/automation":
					m.automationsPage(c, true)
				case "/automation/new":
					m.automationCreatePage(c)
				case "/options":
					m.optionsView(c)
				case "/options/model":
					m.modelOptionsView(c)
				case "/options/effort":
					m.effortOptionsView(c)
				case "/options/permissions":
					m.permissionOptionsView(c)
				case "/web":
					m.webPage(c)
				case "/approval":
					m.approvalPage(c)
				case "/desktop":
					m.DesktopPageView(c)
				case "/desktop/add":
					m.addDesktopView(c)
				default:
					page.Page().Padding(0)
					m.listPage(c, "/")
				}
			})
			m.Router().Transition = transition
			// Leaving the remote desktop page ends its lease.
			// Push and Back update the path at once, so this runs as the page is left.
			// A floating session keeps its lease until closed or restored.
			if m.DesktopPage != nil && !m.DesktopPage.Floating && m.Router().Path() != "/desktop" {
				m.CloseDesktopPage(false)
			}
		}
	})
	m.conversationActionsSheet(c)
	m.desktopFloatView(c)
	// Release the conversation after navigation reaches the new-conversation page.
	if m.Nav != nil && m.Nav.Path() == "/" && !m.Nav.Transitioning() && (m.Open != 0 || m.NewProject != "") {
		m.leave()
		c.Invalidate()
	}
}

// Derive the label from the retry deadline so a delayed frame or a return
// from the background catches up immediately, instead of counting ticks.
func (m *App) updateReconnectCountdown(c *ui.Context) {
	if m.RetryAt.IsZero() {
		return
	}
	remaining := m.RetryAt.Sub(c.Now())
	if remaining <= 0 {
		m.Status = tr("Reconnecting to desktop…")
		return
	}
	seconds := int((remaining + time.Second - 1) / time.Second)
	m.Status = fmt.Sprintf(tr("Disconnected. Retrying in %d seconds."), seconds)
	if m.RetryError != "" {
		m.Status += ": " + m.RetryError
	}
	c.After(remaining - time.Duration(seconds-1)*time.Second)
}

// circle is a round floating button, as the header's.
func (m *App) circle(c *ui.Context, key, label string, child func()) ui.Element {
	b := m.phoneButton(c.Key(key)).Size(44, 44).Padding(0).Radius(22).Background(m.Pal.surface).Justify(ui.Center).Label(label)
	b.Children(child)
	return b
}

// pill is a rounded text button; primary fills it with the ink color.
func (m *App) pill(c *ui.Context, key, label string, primary bool) ui.Element {
	bg, fg := m.Pal.surface, m.Pal.ink
	if primary {
		bg, fg = m.Pal.ink, m.Pal.Bg
	}
	b := m.phoneButton(c.Key(key)).MinHeight(44).Padding(10, 16).Radius(22).Background(bg).Justify(ui.Center).Label(label)
	b.Children(func() { ui.Text(c, label).FontSize(m.Font(14)).FontWeight(500).TextColor(fg) })
	return b
}

func (m *App) menuIcon(c *ui.Context) {
	ui.Column(c).Gap(4).Children(func() {
		for i := range 3 {
			ui.Box(c.Key(i)).Size(18, 2).Radius(1).Background(m.Pal.ink)
		}
	})
}

func (m *App) pairView(c *ui.Context) {
	p := m.Pal
	ui.Column(c).Grow(1).Justify(ui.Center).Gap(14).Padding(32).Children(func() {
		ui.Row(c).Size(64, 64).Radius(18).Background(p.surface).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			widgets.Icon(c, "terminal-2", 30, p.ink)
		})
		ui.Text(c, tr("Connect to Miya")).FontSize(m.Font(26)).FontWeight(700).Margin(10, 0, 0, 0)
		ui.Text(c, tr("Open Miya on your desktop, enable mobile connections in Settings → Phone, then scan the QR code.")).FontSize(m.Font(15)).TextColor(p.muted).LineHeight(1.5)
		scan := m.phoneButton(c.Key("scan")).FillWidth().MinHeight(52).Radius(16).Background(p.ink).Justify(ui.Center).Gap(8).Label(tr("Scan to connect"))
		scan.Children(func() {
			if m.scanning {
				ui.Spinner(c).TextColor(p.Bg)
			}
			ui.Text(c, tr("Scan to connect")).FontSize(m.Font(16)).FontWeight(600).TextColor(p.Bg)
		})
		if scan.Clicked() {
			m.scan()
		}
		ui.Row(c).FillWidth().MinHeight(52).Padding(0, 6, 0, 16).Gap(8).AlignItems(ui.Center).Background(p.surface).Radius(16).Children(func() {
			input := ui.TextInput(c.Key("paste"), &m.Paste).Label(tr("Desktop connection code")).Placeholder(tr("Or paste a connection code")).Grow(1).MinWidth(0).Background(p.surface).Border(0, p.surface).
				Keyboard(ui.KeyboardASCII).Autocorrect(false).Capitalize(ui.CapitalizeNone).ReturnKey(ui.ReturnGo)
			m.pairInput.restore(input)
			if m.pill(c, "paste-code", tr("Paste"), false).KeepFocus().Clicked() {
				m.pairInput.paste(c, input, &m.Paste, false)
			}
			if m.pill(c, "paste-connect", tr("Connect"), false).Background(p.Bg).Clicked() {
				m.pair(m.Paste)
			}
		})
		if m.err != "" {
			ui.Text(c, m.err).TextColor(widgets.Danger).FontSize(m.Font(14))
		}
		if m.desktopLoadFailed {
			if m.pill(c, "reload-desktops", tr("Reload desktops"), false).Clicked() {
				go m.loadSaved()
			}
		}
	})
}

func (m *App) listView(c *ui.Context) {
	p := m.Pal
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(12).AlignItems(ui.Center).Children(func() {
		if m.homeAction(c, "menu", tr("Desktop connections"), "layout-sidebar").Clicked() {
			m.Menu = !m.DrawerVisible
		}
		ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(3).Children(func() {
			ui.Text(c, "Miya").FontSize(m.Font(16)).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				dot, label := p.muted, m.ConnectionLine()
				if label == "" {
					dot, label = p.ok, m.DesktopName()
					if label == "" {
						label = tr("Connected")
					}
				}
				ui.Box(c.Key("dot")).Size(6, 6).Radius(3).Background(dot)
				ui.Text(c, strings.TrimSuffix(label, ".local")).FontSize(m.Font(11)).TextColor(p.muted).SingleLine().Ellipsis("…")
			})
		})
		more := m.homeAction(c, "more", tr("More"), "dots")
		m.conversationActions(c, more, m.selectedProject().Path, m.composerAgent())
	})
	if len(m.State.Projects) == 0 {
		ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if m.Connected {
				ui.Text(c, tr("No projects on this desktop yet")).TextColor(p.muted)
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
func (m *App) selectedProject() model.ProjectSnapshot {
	if m.Selected != "" {
		for _, p := range m.State.Projects {
			if p.Path == m.Selected {
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
func (m *App) listPage(c *ui.Context, path string) {
	p := m.Pal
	safe := safeAreaOf(c)
	screen := c.Root().Bounds().W
	w := min(float32(320), screen*0.84)
	m.DrawerW = w
	if m.Link == "" {
		m.Menu = false
	}
	want := float32(0)
	if m.Menu {
		want = w
	}
	if !m.DrawerDragging && m.DrawerOffset.X != want {
		// The menu button, a project or the scrim moved it: animate there.
		m.DrawerOffset = ui.Point{X: want}
	}
	ui.Box(c.Key("drawer-clip")).Fill().Clip().Children(func() {
		row := ui.Row(c.Key("drawer-row")).Width(w+screen).Shrink(0).FillHeight().Margin(0, 0, 0, -w).AlignItems(ui.Stretch).Background(p.Drawer)
		row.Draggable(&m.DrawerOffset, ui.DraggableOptions{
			Axis:      ui.Horizontal,
			Bounds:    &ui.Rect{W: w},
			Snap:      ui.Nearest(ui.Point{}, ui.Point{X: w}),
			Animation: drawerMotion,
		})
		shown := max(0, min(1, row.DragOffset().X/w))
		if _, _, ended := row.DragEnded(); ended {
			m.Menu = m.DrawerOffset.X > w/2
		}
		m.DrawerDragging = row.Dragging()
		m.DrawerVisible = shown > 0.001
		quotaShown := m.DrawerVisible || m.Menu || m.DrawerDragging
		if quotaShown && !m.quotaDrawerShown && m.Connected && m.Conn != nil {
			m.sendPhoneAction(remote.Request{Op: "quota"})
		}
		if !quotaShown {
			m.QuotaOpen = false
		}
		m.quotaDrawerShown = quotaShown
		row.Children(func() {
			ui.Column(c.Key("drawer")).Width(w).FillHeight().Background(p.Drawer).Padding(safe.Top+12, 24, safe.Bottom+16, safe.Left+24).Gap(12).Children(func() {
				if m.DrawerVisible || m.Menu || m.DrawerDragging {
					m.drawerContent(c)
				}
			})
			background := m.HomeBackground()
			if path != "/" {
				background = p.Bg
			}
			if mobileSettingsPage(path) {
				background = m.settingsBackground()
			}
			if m.Dark {
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
				if m.DrawerVisible {
					// The scrim covers the list: a tap on it closes the drawer,
					// a drag on it moves the drawer.
					ui.Box(c.Key("scrim")).Label(tr("Close sidebar")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, 0.45*shown))
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
		if m.DrawerVisible {
			pressed := row.Pressed()
			clicked := row.Clicked()
			if pressed && !m.rowPressed {
				m.pressX, m.pressOpen = -1, m.Menu
				if x, _, ok := row.PointerPosition(); ok {
					m.pressX = x
				}
			}
			released := m.rowPressed && !pressed
			m.rowPressed = pressed
			if m.pressOpen && m.pressX >= w && (clicked || released) {
				m.Menu, m.pressOpen = false, false
			}
		} else {
			m.rowPressed, m.pressOpen = false, false
		}
	})
	// Buttons and scrim taps commit during this frame; drags commit on
	// release. Give feedback now, without waiting for the slide to finish.
	if !m.DrawerDragging && m.drawerFeedbackOpen != m.Menu {
		m.drawerFeedbackOpen = m.Menu
		mobileFeedback(mygo.Haptics.Light)
	}
}

// drawerContent is the drawer: the desktop, its agent quotas, the
// projects to show, and the connection's actions.
func (m *App) drawerContent(c *ui.Context) {
	p := m.Pal
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		m.drawerDesktopSwitcher(c)
		label := tr("Search conversations")
		if m.DrawerSearch {
			label = tr("Close search")
		}
		if m.circle(c, "search", label, func() { widgets.Icon(c, "search", 22, p.ink) }).Clicked() {
			m.DrawerSearch = !m.DrawerSearch
			m.DrawerQuery = ""
		}
	})
	if m.DrawerSearch {
		search := ui.TextInput(c.Key("drawer-query"), &m.DrawerQuery).FillWidth().MinHeight(44).Padding(10, 12).Radius(12).
			Background(p.surface).Border(0, p.surface).FontSize(m.Font(16)).Label(tr("Search conversations")).Placeholder(tr("Search conversation titles")).AutoFocus().
			ReturnKey(ui.ReturnSearch).Capitalize(ui.CapitalizeNone).KeyboardDismiss(ui.KeyboardDismissInteractive)
		if search.Submitted() {
			c.Blur()
		}
	}
	if m.err != "" {
		ui.Text(c, m.err).FontSize(m.Font(13)).TextColor(widgets.Danger).LineHeight(1.4)
	}
	m.drawerQuotas(c.Key("drawer-quotas"))

	// Reserve touch drags before the conversation buttons take their presses.
	// TouchScroll replays stationary taps to the buttons and sends swipes as
	// precise scrolls; vertical scrolls continue to the list normally.
	ui.Scroll(c.Key("drawer-projects")).FillWidth().Grow(1).MinHeight(0).TouchScroll().HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputScroll && ev.Precise && !ev.Momentum && ev.DX > 8 && ev.DX > max(ev.DY, -ev.DY) {
			m.Menu = false
			return true
		}
		return false
	}).Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			if m.DrawerSearch {
				matches := appmodel.MatchingThreadRefs(m.allPhoneProjects(), 0, strings.TrimSpace(m.DrawerQuery))
				if len(matches) == 0 {
					ui.Text(c, tr("No matching conversations")).Padding(12, 0).FontSize(m.Font(14)).TextColor(p.muted)
				}
				for _, match := range matches {
					m.drawerThread(c.Key(match.ID), match.Thread)
				}
				return
			}
			if m.phoneButton(c.Key("memory")).FillWidth().MinHeight(48).Padding(0).Gap(12).Label(tr("Shared memory")).Children(func() {
				widgets.Icon(c, "stack-2", 21, p.ink)
				ui.Text(c, tr("Shared memory")).FontSize(m.Font(17))
			}).Clicked() {
				m.Memories.RequestedAt = time.Time{}
				m.OpenDrawerPage("/memory")
			}
			automations := m.phoneButton(c.Key("automations")).FillWidth().MinHeight(48).Padding(0).Gap(12).Radius(12).Label(tr("Automations"))
			automations.Children(func() {
				widgets.Icon(c, "clock", 21, p.ink)
				ui.Text(c, tr("Automations")).FontSize(m.Font(17)).FontWeight(400)
			})
			if automations.Clicked() {
				m.Autos.RequestedAt = time.Time{}
				m.OpenDrawerPage("/automations")
			}
			ui.Box(c.Key("navigation-separator")).FillWidth().Height(1).Margin(16, 0, 12, 0).Background(p.line)
			m.drawerActivity(c.Key("activity"))
			ui.Text(c, tr("Projects")).FontSize(m.Font(13)).TextColor(p.muted).Margin(0, 0, 6, 0)
			if !m.projectsExpanded && len(m.State.Projects) > 0 {
				m.ExpandedProject, m.projectsExpanded = m.selectedProject().Path, true
			}
			for _, pr := range m.State.Projects {
				m.drawerProject(c.Key("drawer:"+pr.Path), pr)
			}
		})
	})
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		newThread := m.phoneButton(c.Key("new-thread")).MinWidth(0).Shrink(1).MinHeight(48).Padding(0, 20).Gap(10).Radius(24).Background(p.ink).TextColor(p.Bg).Label(tr("New thread"))
		newThread.Children(func() {
			widgets.Icon(c, "edit", 21, p.Bg)
			label := ui.Text(c, tr("New thread")).FontSize(m.Font(16)).FontWeight(600).MinWidth(0).Shrink(1).Ellipsis("…")
			if m.textScale > 1.3 {
				label.MaxLines(2)
			} else {
				label.SingleLine()
			}
		})
		if newThread.Clicked() {
			m.StartConversation(m.selectedProject().Path, m.composerAgent())
		}
		ui.Spacer(c)
		if m.circle(c, "settings", tr("Settings"), func() { widgets.Icon(c, "settings", 22, p.ink) }).Size(48, 48).Radius(24).Clicked() {
			m.OpenDrawerPage("/settings")
		}
	})
}

// drawerMotion settles the drawer as iOS does: a spring that does not
// bounce, keeping the speed of a throw.
var drawerMotion = ui.Animation{Spring: ui.SpringOptions{Duration: 380 * time.Millisecond}}

// showProject selects the phone's workspace and toggles its conversation list.
func (m *App) ShowProject(path string) {
	m.Selected, m.projectsExpanded = path, true
	if m.ExpandedProject == path {
		m.ExpandedProject = ""
	} else {
		m.ExpandedProject = path
	}
}

// threadStatus marks conversations waiting for approval, running, or unread.
func (m *App) threadStatus(c *ui.Context, t model.Thread) {
	p := m.Pal
	switch {
	case len(m.ApprovalsFor(t.ServerID)) > 0:
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Box(c.Key("dot")).Size(7, 7).Radius(4).Background(p.warn)
			ui.Text(c, tr("Pending approval")).FontSize(m.Font(12)).TextColor(p.warn)
		})
	case t.Busy:
		ui.Spinner(c).FontSize(m.Font(13))
	default:
		if m.completionUnread(t) {
			ui.Box(c).Size(8, 8).Radius(4).Background(p.ok)
		}
	}
}

func (m *App) threadView(c *ui.Context) {
	p := m.Pal
	cur := m.Viewer.Current()
	listed, proj := m.find(m.Open)
	title, agent, busy := m.Title, cur.Agent, cur.Busy || listed.Busy
	if listed.Title != "" {
		title = listed.Title
	}
	if agent == "" {
		agent = listed.Agent
	}
	if m.Open == 0 {
		title, agent, busy, proj = tr("New conversation"), m.NewAgent, false, m.project(m.NewProject)
	}
	if agent == "" {
		agent = m.defaultAgent()
	}
	if title == "" {
		title = tr("Conversations")
	}
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/thread")
		ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(2).Children(func() {
			ui.Text(c, msgfmt.NativeText(title)).FontSize(m.Font(16)).FontWeight(600).SingleLine().Ellipsis("…")
			sub := appmodel.AgentName(agent)
			if proj.Name != "" {
				sub += " · " + proj.Name
			}
			if line := m.ConnectionLine(); line != "" {
				sub = line
			}
			ui.Text(c, sub).FontSize(m.Font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
		})
		more := m.circle(c, "more", tr("More"), func() { widgets.Icon(c, "dots", 20, p.ink) })
		m.conversationActions(c, more, proj.Path, agent)
	})
	// In a conversation the messages scroll under the composer, which
	// floats over their bottom; a new or pending one keeps it below them.
	pendingThread, pending := m.PendingSendThread(cur)
	floating := !pending && m.Open != 0
	// The bar reaches under the home indicator, past the bottom of the
	// messages, which keep room only for what is above it.
	safe := safeAreaOf(c)
	m.View.BottomInset = 0
	if floating {
		// The fade over the bar's top may cover the list's own padding.
		m.View.BottomInset = max(0, m.composerH-safe.Bottom-composerFade)
	}
	ui.Column(c.Key("conversation-body")).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			if pending {
				m.View.MessageList(c, pendingThread)
				m.pendingSendStatus(c)
			} else if m.Open == 0 {
				m.newConversationView(c, proj)
			} else {
				m.View.MessageList(c, cur)
			}
		})
		bar := ui.Column(c.Key("composer-overlay")).FillWidth()
		if floating {
			// What scrolls under the bar fades out over its top, so the
			// turn's status and the composer read on the page's own color.
			fade := min(1, 28/max(m.composerH, 28))
			bar.Absolute().Left(0).Right(0).Bottom(-safe.Bottom).Padding(composerFade, 0, safe.Bottom, 0).
				LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
					{Color: p.Bg.Alpha(0), At: 0}, {Color: p.Bg.Alpha(0.85), At: fade * 0.6}, {Color: p.Bg, At: fade},
				}})
		}
		bar.Children(func() {
			m.View.Footer()
			for _, a := range m.ApprovalsFor(cur.ServerID) {
				ui.Column(c.Key("approval:" + string(a.ID))).FillWidth().Children(func() { m.approvalCard(c, a) })
			}
			m.composer(c, agent, busy)
		})
		h := float32(0)
		if floating {
			h = bar.LayoutBounds().H
		}
		if h != m.composerH {
			m.composerH = h
			c.After(0)
		}
	})
}

func (m *App) newConversationView(c *ui.Context, proj model.ProjectSnapshot) {
	p := m.Pal
	agent := m.composerAgent()
	ui.Scroll(c.Key("draft-intro")).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().MinHeight(220).AlignItems(ui.Center).Justify(ui.Center).Gap(14).Padding(24).Children(func() {
			widgets.AgentIcon(c, agent, 46)
			ui.Text(c, tr("Let’s build")).FontSize(m.Font(28)).FontWeight(600)
			pick := m.phoneButton(c.Key("pick-project")).MinHeight(44).Gap(8).Label(tr("Select project"))
			pick.Children(func() {
				ui.Text(c, proj.Name).FontSize(m.Font(24)).TextColor(p.muted).SingleLine().Ellipsis("…")
				widgets.Icon(c, "chevron-down", 16, p.muted)
			})
			pick.PopoverMenuButton(func(menu *ui.Menu) {
				for _, project := range m.State.Projects {
					if menu.Item(project.Name).Radio(project.Path == proj.Path).Chosen() {
						m.Selected = project.Path
						m.ExpandedProject, m.projectsExpanded = project.Path, true
						if m.NewProject != "" {
							m.NewProject = project.Path
						}
					}
				}
			})
			ui.Row(c).FillWidth().Wrap().Justify(ui.Center).Gap(8).Children(func() {
				for _, descriptor := range m.agentDescriptors() {
					a := descriptor.ID
					if m.pill(c, "agent:"+a, descriptor.Name, agent == a).Clicked() {
						m.NewAgent = a
					}
				}
			})
		})
	})
}

func (m *App) composerAgent() string {
	if m.NewAgent != "" {
		return m.NewAgent
	}
	if m.State.Agent != "" {
		return m.State.Agent
	}
	return m.defaultAgent()
}

// composerFade is the room over the floating composer where the messages
// scrolling under it fade out.
const composerFade = 18

// composer is the floating message box: the agent, the text, the model's
// effort, and a button that sends, or stops a running turn.
func (m *App) composer(c *ui.Context, agent string, busy bool) {
	p := m.Pal
	m.InlineInput.Commands, _ = m.composerCommands(agent, m.ComposerProject())
	owner := fmt.Sprintf("%s:%d:%s", m.ComposerProject(), m.Open, agent)
	if m.InlineInput.Doc != nil && m.InlineInput.Owner == owner {
		m.InlineInput.Sync(m.Draft, func(text string) { m.Draft = text })
	}
	topPadding := float32(6)
	if len(m.Outbox) > 0 || m.View.TurnStatusVisible(m.Viewer.Current()) {
		topPadding = 0
	}
	ui.Column(c).FillWidth().Padding(topPadding, 12, 10, 12).Gap(6).Children(func() {
		m.voiceStatus(c)
		if m.SendErr != "" {
			ui.Text(c, m.SendErr).FontSize(m.Font(13)).TextColor(widgets.Danger).Margin(0, 8)
		}
		m.commandMenu(c, agent, m.ComposerProject())
		m.threadMenu(c, m.ComposerProject())
		m.queueView(c)
		m.taskStatus(c)
		m.collaborationTasks(c)
		m.thumbnails(c)
		if hint := m.InlineInput.CommandHint(); hint != "" {
			ui.Text(c, hint).FontSize(m.Font(12)).TextColor(p.muted).FillWidth().Wrap().Padding(0, 10)
		}
		// A little translucent: what scrolls under the composer shows through.
		ui.Column(c.Key("composer-box")).FillWidth().Padding(6, 6, 6, 10).Background(p.surface.Alpha(0.85)).Radius(26).Children(func() {
			opts := m.SendOptions(agent)
			effort := strings.ToUpper(textutil.FirstNonEmpty(opts.Effort, tr("Default")))
			hasDraft := strings.TrimSpace(m.Draft) != "" || len(m.Images) > 0
			steerAvailable := busy && hasDraft
			showEffort := effort != "" && c.Root().Bounds().W >= 360 && m.textScale <= 1.3 && !steerAvailable
			right := float32(52)
			if steerAvailable {
				right += 72
			}
			if showEffort {
				w, _ := c.MeasureText(0, ui.Span{Text: effort, Size: m.Font(12)})
				right += max(44, w) + 8
			}
			safe := safeAreaOf(c)
			width := c.Root().Bounds().W - safe.Left - safe.Right - 24 - 16 - 90 - right
			style := conversation.MentionStyle(c, m.Font(16))
			m.InlineInput.Style, m.InlineInput.Title = style, m.View.ReferenceTitle
			text := m.InlineInput.Text(m.Draft)
			_, h := ui.ShapeRichText(conversation.InlineThreadSpans(text, m.Font(16)), style.Font, max(1, width)).Size()
			_, line := c.MeasureText(0, ui.Span{Text: "M", Size: m.Font(16)})
			expanded := strings.Contains(m.Draft, "\n") || h > line*1.5 || steerAvailable
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
				placeholder := appmodel.AgentName(agent)
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
					m.InlineInput.InputHook = m.voiceInput
					input := m.View.ThreadInput(c.Key("composer"), &m.InlineInput, m.Draft, owner, tr("Message"), placeholder, m.Font(16), inputWidth, 1, 6, func(text string) { m.Draft = text })
					// EditorBase padding does not inset its text. Center it
					// using the enclosing column instead.
					input.MinHeight(0).Padding(0)
				})
			})
			ui.Row(c.Key("left-tools")).Absolute().Left(4).Bottom(6).Width(88).MinHeight(44).AlignItems(ui.Center).Children(func() {
				// Bring the icons closer within separate 44-point hit areas.
				add := m.phoneButton(c.Key("attach")).Size(44, 44).Padding(0, 6, 0, 0).Justify(ui.End).Label(tr("Add image"))
				add.Children(func() { widgets.Icon(c, "plus", 20, p.muted) })
				if add.Clicked() {
					m.addPhotos()
				}
				agentButton := m.phoneButton(c.Key("agent")).Size(44, 44).Padding(0, 0, 0, 6).Justify(ui.Start).Label(tr("Model settings"))
				agentButton.Children(func() { widgets.AgentIcon(c, agent, 20) })
				if agentButton.Clicked() {
					m.openOptions(agent)
				}
			})
			ui.Row(c.Key("right-tools")).Absolute().Right(6).Bottom(6).Width(right - 8).MinHeight(44).Gap(8).AlignItems(ui.Center).Children(func() {
				if steerAvailable {
					b := m.phoneButton(c.Key("steer")).Size(64, 44).Padding(0).Justify(ui.Center).Label(tr("Steer now")).Disabled(m.Sending || m.voice != nil)
					b.Children(func() { ui.Text(c, tr("Steer")).FontSize(m.Font(12)).TextColor(p.ink) })
					if b.Clicked() {
						m.sendDraft(true)
					}
				}
				if showEffort {
					b := m.phoneButton(c.Key("effort")).MinHeight(44).Shrink(0).Padding(0).Label(tr("Reasoning effort"))
					b.Children(func() { ui.Text(c, effort).FontSize(m.Font(12)).TextColor(p.muted) })
					if b.Clicked() {
						m.openOptions(agent)
					}
				}
				canSend := m.Connected && !m.Sending && m.voice == nil && (strings.TrimSpace(m.Draft) != "" || len(m.Images) > 0)
				bg, fg, name, label := p.line, p.muted, "arrow-up", tr("Send")
				if busy && !hasDraft {
					bg, fg, name, label = p.ink, p.Bg, "x", tr("Stop")
				} else if busy {
					bg, fg, label = p.ink, p.Bg, tr("Add to queue")
				} else if canSend {
					bg, fg = p.ink, p.Bg
				}
				b := m.phoneButton(c.Key("send")).Size(44, 44).Shrink(0).Padding(0).Radius(22).Background(bg).Justify(ui.Center).Label(label).Disabled(m.voice != nil || m.Sending)
				b.Children(func() {
					if m.Sending {
						ui.Spinner(c).FontSize(m.Font(12)).TextColor(fg)
					} else {
						widgets.Icon(c, name, 18, fg)
					}
				})
				if b.Clicked() {
					if busy && !hasDraft {
						m.stop()
					} else if canSend {
						m.Send()
					}
				}
			})
		})
	})
}

// addPhotos picks photos from the library with the system picker, which
// needs no access to the whole library.
func (m *App) addPhotos() {
	if len(m.Images) >= phonetransfer.MaxImages {
		m.SendErr = fmt.Sprintf(tr("Send up to %d images at a time"), phonetransfer.MaxImages)
		return
	}
	go func() {
		paths, err := imagepicker.Pick(context.Background(), imagepicker.Options{Limit: -1})
		uiruntime.RunOnMain(func() {
			switch {
			case errors.Is(err, mygo.ErrUnsupported):
				m.SendErr = tr("This device does not support image selection")
			case err != nil:
				m.SendErr = tr("Unable to open photos: ") + err.Error()
			}
			for _, p := range paths {
				if len(m.Images) >= phonetransfer.MaxImages {
					m.SendErr = fmt.Sprintf(tr("Send up to %d images at a time"), phonetransfer.MaxImages)
					go os.Remove(p)
					continue
				}
				m.Images = append(m.Images, p)
			}
		})
	}()
}

func (m *App) removeImage(i int) {
	if i < 0 || i >= len(m.Images) {
		return
	}
	p := m.Images[i]
	m.Images = append(m.Images[:i:i], m.Images[i+1:]...)
	go os.Remove(p)
}

// clearImages drops the attached photos and their cache copies.
func (m *App) clearImages() {
	for _, p := range m.Images {
		go os.Remove(p)
	}
	m.Images = nil
}

// thumbnails shows the photos to send above the composer.
func (m *App) thumbnails(c *ui.Context) {
	if len(m.Images) == 0 {
		return
	}
	p := m.Pal
	ui.Row(c.Key("thumbs")).Gap(8).Padding(0, 4).Children(func() {
		for i, path := range m.Images {
			ui.Row(c.Key("thumb:"+path)).Size(64, 64).Radius(12).Background(p.surface).Clip().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				if img := widgets.CachedImage(path); img != nil {
					ui.Image(c.Key("img"), img).Size(64, 64).Fit(ui.Cover)
				} else {
					widgets.Icon(c, "file", 22, p.muted)
				}
				rm := m.phoneButton(c.Key("rm")).Absolute().Top(0).Right(0).Size(44, 44).Padding(0).Justify(ui.Center).Label(tr("Remove image"))
				rm.Children(func() {
					ui.Box(c).Size(22, 22).Radius(11).Background(ui.RGBA(0, 0, 0, 0.6)).AlignItems(ui.Center).Justify(ui.Center).Children(func() { widgets.Icon(c, "x", 12, ui.Hex("#ffffff")) })
				})
				if rm.Clicked() {
					m.removeImage(i)
				}
			})
		}
	})
}

// composerProject is the project of the conversation being written in.
func (m *App) ComposerProject() string {
	if m.Open != 0 {
		if _, p := m.find(m.Open); p.Path != "" {
			return p.Path
		}
	}
	if m.NewProject != "" {
		return m.NewProject
	}
	return m.selectedProject().Path
}

// requestCommands asks the desktop for a project's Claude Code commands,
// once per connection.
func (m *App) requestCommands(id, project string) {
	conn := m.Conn
	key := agent.CommandKey(id, project)
	if project == "" || conn == nil || m.Asked[key] {
		return
	}
	if m.Asked == nil {
		m.Asked = map[string]bool{}
	}
	m.Asked[key] = true
	go conn.Send(remote.Request{Op: "commands", Agent: id, Project: project})
}

func (m *App) composerCommands(id, project string) ([]model.ComposerChoice, bool) {
	if m.agentDescriptor(id).NativeCommands {
		return m.commandChoices(id, project)
	}
	for _, option := range m.State.Agents {
		if option.Agent == id && option.Commands != nil {
			return option.Commands, true
		}
	}
	fallback := agent.DefaultCommands(id)
	return fallback.PhoneChoices, fallback.Known
}

// commandMenu lists commands while the draft is a slash and a command's beginning.
func (m *App) commandMenu(c *ui.Context, agent, project string) {
	d := m.Draft
	if !strings.HasPrefix(d, "/") || strings.ContainsAny(d, " \n") {
		return
	}
	p := m.Pal
	q := strings.ToLower(d[1:])
	if m.agentDescriptor(agent).NativeCommands {
		m.requestCommands(agent, project)
	}
	cmds, known := m.composerCommands(agent, project)
	var shown []model.ComposerChoice
	for _, cmd := range cmds {
		if strings.Contains(strings.ToLower(cmd.Name), q) {
			shown = append(shown, cmd)
		}
	}
	ui.Column(c.Key("commands")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
		switch {
		case !known:
			ui.Row(c).Gap(8).Padding(10).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).FontSize(m.Font(12))
				ui.Text(c, tr("Loading commands…")).FontSize(m.Font(13)).TextColor(p.muted)
			})
		case len(shown) == 0:
			ui.Text(c, tr("No matching commands")).FontSize(m.Font(13)).TextColor(p.muted).Padding(10)
		default:
			ui.Scroll(c.Key("command-list")).FillWidth().MaxHeight(260).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for _, cmd := range shown {
						item := m.phoneButton(c.Key("cmd:"+cmd.Name)).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label("/" + cmd.Name)
						item.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
								ui.Text(c, "/"+cmd.Name).FontSize(m.Font(15)).FontWeight(500).SingleLine().Ellipsis("…")
								if cmd.Detail != "" {
									ui.Text(c, cmd.Detail).FontSize(m.Font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
								}
							})
						})
						if item.Clicked() {
							m.Draft = "/" + cmd.Name + " "
						}
					}
				})
			})
		}
	})
}

// phoneCachedThreads bounds how many conversations the phone keeps.
const phoneCachedThreads = 30

func (m *App) CachedMessages(serverID string) []model.Message {
	if serverID == "" {
		return nil
	}
	if msgs, ok := m.MsgCache[serverID]; ok {
		return msgs
	}
	msgs := m.loadDisk(serverID)
	if len(msgs) > 0 {
		m.keep(serverID, msgs)
	}
	return msgs
}

// remember keeps a conversation's messages, dropping the oldest kept.
func (m *App) Remember(serverID string, msgs []model.Message) {
	m.keep(serverID, msgs)
	m.persist(serverID, msgs)
}

// keep holds a conversation's messages in memory, dropping the oldest.
func (m *App) keep(serverID string, msgs []model.Message) {
	if serverID == "" {
		return
	}
	if m.MsgCache == nil {
		m.MsgCache = map[string][]model.Message{}
	}
	for i, k := range m.msgKeys {
		if k == serverID {
			m.msgKeys = append(m.msgKeys[:i:i], m.msgKeys[i+1:]...)
			break
		}
	}
	m.msgKeys = append(m.msgKeys, serverID)
	m.MsgCache[serverID] = msgs
	for len(m.msgKeys) > phoneCachedThreads {
		delete(m.MsgCache, m.msgKeys[0])
		m.msgKeys = m.msgKeys[1:]
	}
}

// subscribeRequest subscribes to a conversation, saying what the phone
// holds of it so the desktop sends only the rest.
func (m *App) SubscribeRequest(id int) remote.Request {
	r := m.ThreadRequest("subscribe", id)
	if id == 0 {
		return r
	}
	for _, msg := range m.CachedMessages(r.ServerID) {
		r.Have = append(r.Have, remote.Seen{ID: msg.ID, FP: svcproto.MessageFingerprint(msg)})
	}
	return r
}

func (m *App) ThreadRequest(op string, id int) remote.Request {
	r := remote.Request{Op: op, Thread: id}
	if id == 0 {
		return r
	}
	t, p := m.find(id)
	// The currently displayed page is authoritative when a new state has
	// already reused its numeric ID for a different conversation.
	if cur := m.Viewer.Current(); cur.ID == id && cur.ServerID != "" {
		if cur.ServerID != t.ServerID || model.ThreadAgent(cur) != model.ThreadAgent(t) {
			p = model.ProjectSnapshot{}
		}
		t = cur
	}
	if _, project, ok := m.referencedThread(model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}); ok {
		p = project
	}
	r.ServerID, r.Agent, r.Project = t.ServerID, model.ThreadAgent(t), p.Path
	return r
}

// applyThread merges a delta into what the phone holds of the conversation.
func (m *App) ApplyThread(d phoneproto.ThreadDelta) {
	t := d.Thread
	var base []model.Message
	if !d.Reset {
		boundIdentity := false
		if cur := m.Viewer.Current(); cur.ID == t.ID && (cur.ServerID == t.ServerID || cur.ServerID == "" && model.ThreadAgent(cur) == model.ThreadAgent(t)) {
			// A new thread's first messages can arrive before its durable ID.
			// Binding that ID changes metadata, not the history already received
			// under its numeric ID; the desktop need not resend those messages.
			base = cur.Messages
			boundIdentity = cur.ServerID == "" && t.ServerID != ""
		}
		if len(base) == 0 {
			base = m.CachedMessages(t.ServerID)
		}
		// Busy, loading, usage and title updates carry no new messages.
		// Reuse the history and its presentation groups instead of copying,
		// regrouping and persisting the entire conversation on each update.
		if len(d.Changed) == 0 && appmodel.SameMessageOrder(base, d.Order) {
			t.Messages = base
			if boundIdentity {
				m.Remember(t.ServerID, base)
			}
			m.updateThreadView(t, false)
			return
		}
	}
	known := make(map[int]model.Message, len(base)+len(d.Changed))
	for _, x := range base {
		known[x.ID] = x
	}
	for _, x := range d.Changed {
		known[x.ID] = x
	}
	list := make([]model.Message, 0, len(d.Order))
	for _, id := range d.Order {
		if x, ok := known[id]; ok {
			list = append(list, x)
		}
	}
	t.Messages = list
	m.Remember(t.ServerID, list)
	m.ShowThread(t)
}

func (m *App) allPhoneProjects() []model.ProjectSnapshot { return m.State.Projects }
