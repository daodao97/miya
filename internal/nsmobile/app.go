// Package nsmobile is the NSGo phone surface. Protocol and conversation state
// live in internal/phone/client; this package owns retained views and signals.
package nsmobile

import (
	"context"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/nsrequests"
	"miya/internal/nswidgets"
	phoneclient "miya/internal/phone/client"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/prefs"
	"miya/internal/remote"
	"miya/locales"
)

func tr(s string) string { return locales.T(s) }

type App struct {
	*phoneclient.Client
	cx               *core.Scope
	revision         *core.Signal[uint64]
	search, pairCode *core.Signal[string]
	pages            *pageStack
	// The drawer: whether it is open, how far it is out, whether it
	// searches, the animation settling it, and whether it has unfolded the
	// selected project yet.
	drawerOpen, drawerSearching, drawerActive *core.Signal[bool]
	drawerPos                                 *core.AnimatedValue
	drawerRun                                 *core.AnimationRun
	drawerAt                                  float64
	drawerExpanded                            bool
	scanning                                  bool
	optionsAgent                              string
	filesURL                                  *core.Signal[string]
	closeFiles                                func()
	caret, anchor                             int
	voice                                     *phoneVoice
	voiceStart                                voiceStart
	voiceAutoSend                             bool
	push                                      phonePush
	web                                       *webServices
	fetchRuntime                              runtimeFetcher
	openWebProxy                              webProxyOpener
	webBrowser                                bool
	collaborations                            nsrequests.CollaborationUI
	collaborationOwner                        string
	requestFields                             map[string]map[string]string
	authentication                            desktopAuthentication
}

func NewMobileApp() *App {
	m := &App{Client: phoneclient.New(), caret: -1, voiceAutoSend: prefs.Load("phone-voice-auto-send") == "on"}
	m.SecretNotFound = core.ErrSecretNotFound
	return m
}

func MobileEntry() {
	nsgo.Run(func(cx *core.Scope) {
		m := NewMobileApp()
		m.DataDir = cx.DataDir()
		nsgo.SetMainView(cx, m.Render)
		cx.OnQuit(func(context.Context) { m.Close() })
	}, nsgo.WindowSize(390, 844))
}

// live binds plain client getters to the adapter's revision. The component
// builds once; only dependent bindings and keyed regions update afterwards.
func live[T any](m *App, get func() T) func() T { return func() T { m.revision.Get(); return get() } }
func (m *App) change() {
	if m.revision != nil {
		m.revision.Update(func(n uint64) uint64 { return n + 1 })
	}
}
func (m *App) act(f func()) func() { return func() { f(); m.change() } }
func (m *App) Render(cx *core.Scope) core.Node {
	m.cx = cx
	m.revision = core.NewSignal(cx, uint64(0))
	m.pages = newPages(cx)
	m.pages.actions = m.showActions
	m.drawerOpen = core.NewSignal(cx, false)
	m.drawerSearching = core.NewSignal(cx, false)
	m.drawerActive = core.NewSignal(cx, false)
	m.drawerPos = core.NewAnimatedValue(cx, 0)
	m.search = core.NewSignal(cx, "")
	m.pairCode = core.NewSignal(cx, "")
	m.filesURL = core.NewSignal(cx, "")
	m.Client.Dispatch = cx.Post
	m.Client.Changed = m.change
	m.setupNative(cx)
	// Drop resolved or superseded form values, including secret answers.
	cx.Effect(func() {
		m.revision.Get()
		active := map[string]bool{}
		for _, a := range m.State.Approvals {
			active[m.requestFieldKey(a)] = true
		}
		for key := range m.requestFields {
			if !active[key] {
				delete(m.requestFields, key)
			}
		}
	})
	cx.SetTheme(nswidgets.Theme())
	cx.SetLocale(prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage")))
	m.setupPush(cx)
	cx.Effect(func() {
		m.revision.Get()
		if v := m.voice; v != nil && !m.voiceValid(v) {
			cx.Post(m.act(m.cancelVoice))
		}
	})
	lifecycle := cx.Env().Lifecycle()
	cx.Effect(func() {
		next := cx.Env().Lifecycle()
		if next == lifecycle {
			return
		}
		prev := lifecycle
		lifecycle = next
		if next == core.LifecycleActive {
			m.refreshDesktopAuthentication()
		}
		if next == core.LifecycleBackground {
			m.cancelVoice()
			m.closeWebServices()
			m.closeFileBrowser()
			m.EnterBackground()
			m.change()
		} else if prev == core.LifecycleBackground && next == core.LifecycleActive {
			m.EnterForeground()
			m.push.foreground.attempted = 0
			m.requestPush(false)
			m.change()
		}
	})
	var opened string
	cx.Effect(func() {
		raw := cx.Env().OpenedURL()
		if raw != "" && raw != opened {
			opened = raw
			if _, _, ok := phoneproto.ParseActivityURL(raw); ok {
				cx.Post(m.act(func() { m.openActivityURL(raw) }))
			} else if _, ok := model.ParseThreadURL(raw); ok {
				cx.Post(m.act(func() { m.openActivityURL(raw) }))
			} else if _, err := remote.ParseLink(raw); err == nil {
				cx.Post(m.act(func() { m.Connect(raw) }))
			}
		}
	})
	// Only a pending reconnect schedules a countdown wake-up.
	cx.Effect(func() {
		m.revision.Get()
		if !m.RetryAt.IsZero() {
			stop := cx.AfterFunc(time.Second, m.change)
			cx.Cleanup(func() { stop() })
		}
	})
	cx.OnBackPress(func() bool {
		if m.drawerOpen.Get() {
			m.drawerOpen.Set(false)
			return true
		}
		// A pushed page is the stack's to pop; its OnDismiss releases it.
		if m.pages.Depth() > 0 {
			return false
		}
		if m.filesURL.Get() != "" {
			m.closeFileBrowser()
			return true
		}
		if m.Path == "/desktop" {
			m.CloseDesktopPage(true)
			m.change()
			return true
		}
		if m.Path != "/" {
			m.Home()
			m.change()
			return true
		}
		return false
	})
	cx.Cleanup(func() {
		m.cancelVoice()
		m.closeWebServices()
		m.closeFileBrowser()
		m.closePush()
		m.CloseDesktopPage(false)
	})
	// Static translated labels are rebuilt only when language changes. Client
	// state and composer drafts stay outside this region.
	return core.Dynamic(func(cx *core.Scope) core.Node {
		tag := cx.Env().Locale()
		if strings.HasPrefix(tag, "zh") {
			tag = "zh-Hans"
		} else {
			tag = "en"
		}
		locales.SetCurrent(tag)
		return m.root(cx)
	})
}
func (m *App) root(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{TestID: "mobile-root", StyleFunc: func() core.Style {
		safe := cx.Env().SafeArea()
		m.revision.Get()
		if m.Path == "/thread" && m.Link != "" && m.filesURL.Get() == "" {
			// FloatingConversation owns the home-indicator inset; the outer
			// surface only keeps it above the keyboard.
			safe.Bottom = cx.Env().KeyboardHeight()
		} else {
			safe.Bottom = max(safe.Bottom, cx.Env().KeyboardHeight())
		}
		return core.Style{Width: core.Pct(100), Height: core.Pct(100), Padding: safe, Background: colors.Canvas(), Color: colors.Ink(), MinHeight: core.Pt(0)}
	}, Children: []core.Node{
		m.stack(cx),
		m.desktopFloat(cx),
	}}
}
func (m *App) page(cx *core.Scope) core.Node {
	// The memo subscribes broadly, but notifies this region only if its key
	// changes, so network deltas keep the transcript and native input mounted.
	type destination struct{ owner, path string }
	key := core.NewMemo(cx, live(m, func() destination {
		if !m.Loaded {
			return destination{path: "loading"}
		}
		if m.Link == "" {
			return destination{path: "pair"}
		}
		if m.filesURL.Get() != "" {
			return destination{path: "files"}
		}
		return destination{owner: m.CurrentDesktopID(), path: m.Path}
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch key.Get().path {
		case "loading":
			return ui.Spinner{}
		case "pair":
			return m.pairView(cx)
		case "files":
			return m.fileView(cx)
		case "/desktop":
			return m.desktopPage(cx)
		case "/thread":
			return m.conversation(cx)
		default:
			return m.home(cx)
		}
	})
}
