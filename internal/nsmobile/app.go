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
	"miya/internal/nswidgets"
	phoneclient "miya/internal/phone/client"
	"miya/internal/prefs"
	"miya/internal/remote"
	"miya/locales"
)

func tr(s string) string { return locales.T(s) }

type App struct {
	*phoneclient.Client
	cx                      *core.Scope
	revision                *core.Signal[uint64]
	sheet, search, pairCode *core.Signal[string]
	optionsAgent            string
	filesURL                *core.Signal[string]
	closeFiles              func()
	caret, anchor           int
	voice                   *phoneVoice
	voiceStart              voiceStart
	voiceAutoSend           bool
	push                    phonePush
	web                     *webServices
	fetchRuntime            runtimeFetcher
	openWebProxy            webProxyOpener
	webBrowser              bool
}

func NewMobileApp() *App {
	return &App{Client: phoneclient.New(), caret: -1, voiceAutoSend: prefs.Load("phone-voice-auto-send") == "on"}
}

func MobileEntry() {
	installNetworkHooks()
	nsgo.Run(func(cx *core.Scope) {
		m := NewMobileApp()
		m.DataDir = cx.DataDir()
		m.PrepareNetwork = PlatformHooks.PrepareNetwork
		// NSGo mobile has no secure-store implementation yet. The adapter reports
		// that explicitly; pairing remains usable for this process without saving
		// secrets to an ordinary file. See TODO.md for the platform contract.
		m.Store = scopeSecrets{cx}
		if PlatformHooks.SecretStore != nil {
			m.Store = PlatformHooks.SecretStore(cx)
		}
		nsgo.SetMainView(cx, m.Render)
		cx.Post(func() { go m.LoadSaved(core.ErrSecretNotFound) })
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
	m.sheet = core.NewSignal(cx, "")
	m.search = core.NewSignal(cx, "")
	m.pairCode = core.NewSignal(cx, "")
	m.filesURL = core.NewSignal(cx, "")
	m.Client.Dispatch = cx.Post
	m.Client.Changed = m.change
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
		if next == core.LifecycleBackground {
			m.cancelVoice()
			m.closeWebServices()
			m.closeFileBrowser()
			m.EnterBackground()
			m.change()
		} else if prev == core.LifecycleBackground && next == core.LifecycleActive {
			m.EnterForeground()
			m.requestPush(false)
			m.change()
		}
	})
	var opened string
	cx.Effect(func() {
		raw := cx.Env().OpenedURL()
		if raw != "" && raw != opened {
			opened = raw
			if _, ok := model.ParseThreadURL(raw); ok {
				cx.Post(m.act(func() { m.openActivityURL(raw) }))
			} else if _, err := remote.ParseLink(raw); err == nil {
				cx.Post(m.act(func() { m.Connect(raw) }))
			}
		}
	})
	stop := cx.ObserveNetworkPath(func(online bool, err error) {
		if err == nil {
			m.NetworkPathChanged(online, "")
			m.change()
		}
	})
	cx.Cleanup(stop)
	// Only a pending reconnect schedules a countdown wake-up.
	cx.Effect(func() {
		m.revision.Get()
		if !m.RetryAt.IsZero() {
			stop := cx.AfterFunc(time.Second, m.change)
			cx.Cleanup(func() { stop() })
		}
	})
	cx.OnBackPress(func() bool {
		if m.sheet.Get() != "" {
			m.closeWebServices()
			m.sheet.Set("")
			return true
		}
		if m.filesURL.Get() != "" {
			m.closeFileBrowser()
			return true
		}
		if m.Path != "/" {
			m.Home()
			m.change()
			return true
		}
		return false
	})
	cx.Cleanup(func() { m.cancelVoice(); m.closeWebServices(); m.closeFileBrowser(); m.closePush() })
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
		m.page(cx),
		ui.Sheet{Open: func() bool { return m.sheet.Get() != "" }, OnClose: func() { m.closeWebServices(); m.sheet.Set("") }, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node { return m.sheetContent(cx) }),
		}},
	}}
}
func (m *App) page(cx *core.Scope) core.Node {
	// The memo subscribes broadly, but notifies this region only if its key
	// changes, so network deltas keep the transcript and native input mounted.
	key := core.NewMemo(cx, live(m, func() string {
		if !m.Loaded {
			return "loading"
		}
		if m.Link == "" {
			return "pair"
		}
		if m.filesURL.Get() != "" {
			return "files"
		}
		return m.Path
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch key.Get() {
		case "loading":
			return ui.Spinner{}
		case "pair":
			return m.pairView(cx)
		case "files":
			return m.fileView(cx)
		case "/thread":
			return m.conversation(cx)
		default:
			return m.home(cx)
		}
	})
}

// TODO(automations, memory, remote-desktop, delegations): add
// owned destinations here alongside their frame handlers in phone/client.
func (m *App) sheetContent(cx *core.Scope) core.Node {
	switch m.sheet.Get() {
	case "web-services":
		return m.webServicesSheet(cx)
	case "voice":
		return m.voiceSettings(cx)
	case "push":
		return m.pushSettings(cx)
	case "settings":
		return m.settings(cx)
	case "language":
		return m.languageSettings(cx)
	case "projects":
		return m.projects(cx)
	case "options", "agent", "model", "effort", "policy":
		return m.options(cx, m.sheet.Get())
	case "actions":
		return m.conversationMenu(cx)
	case "quota":
		return m.quotas(cx)
	case "pair":
		return m.pairView(cx)
	}
	return core.View{}
}
