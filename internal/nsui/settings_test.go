package nsui

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/agent"
	"miya/internal/agent/login"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/nswidgets"
	"miya/internal/prefs"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
	"miya/locales"
)

func startSettings(t *testing.T, h *fakeHost, initial bool) (*nsgotest.App, *core.Signal[bool]) {
	t.Helper()
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	locales.SetCurrent("en")
	t.Cleanup(func() { uiruntime.SetAppWindow(nil); current = nil; locales.SetCurrent("") })
	var open *core.Signal[bool]
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		open = core.NewSignal(cx, initial)
		return core.Group([]core.Node{ui.Button{Text: "Open settings", TestID: "open-settings", OnPress: func() { open.Set(true) }}, settingsDialog(cx, h, open)})
	}, nsgotest.Size(1000, 850), nsgotest.Offscreen())
	return app, open
}
func settingsHost() *fakeHost {
	h := newFakeHost(model.Thread{ID: 1, Title: "Conversation"})
	h.agents = []agent.Availability{{ID: "first", Name: "First", Path: "/bin/first", Enabled: true}, {ID: "second", Name: "Second", Path: "/bin/second", Enabled: true}}
	return h
}
func TestSettingsDialogOpensAndCloses(t *testing.T) {
	app, open := startSettings(t, settingsHost(), false)
	if app.Exists("id:settings") {
		t.Fatal("closed dialog rendered settings")
	}
	app.Press("id:open-settings")
	for _, category := range []string{"general", "account", "phone", "memory"} {
		if !app.Exists("id:settings-nav-" + category) {
			t.Errorf("missing category %s", category)
		}
	}
	if !app.Exists("id:interface-language") || !app.Exists("id:appearance") || !app.Exists("id:notification-completion") {
		t.Fatalf("general settings missing: %s", app.Dump())
	}
	app.Press("id:settings-close")
	if open.GetUntracked() {
		t.Fatal("close left open true")
	}
	if app.Exists("id:settings") {
		t.Fatal("settings remained visible after close")
	}
	app.Press("id:open-settings")
	app.Key("Escape")
	if open.GetUntracked() {
		t.Fatal("Escape left open true")
	}
}
func TestSettingsAgentToggleCallsControllerAndFollowsInvalidate(t *testing.T) {
	h := settingsHost()
	app, _ := startSettings(t, h, true)
	app.Press("id:agent-second")
	if len(h.agentCalls) != 1 || h.agentCalls[0] != "second" || h.agents[1].Enabled {
		t.Fatalf("toggle did not reach controller: %+v", h)
	}
	if !app.Find("id:agent-first").Props.Disabled {
		t.Fatal("last enabled agent should be disabled")
	}
	app.Post(func() { h.agents[1].Enabled = true; uiruntime.Invalidate() })
	app.Settle()
	if app.Find("id:agent-first").Props.Disabled || !app.Find("id:agent-second").Props.Checked {
		t.Fatal("switches did not follow controller change")
	}
}
func TestSettingsPreferencesAndPermissions(t *testing.T) {
	h := settingsHost()
	app, _ := startSettings(t, h, true)
	app.Press("id:notification-completion")
	if prefs.Load("notifications-completion") != "off" {
		t.Fatal("notification preference not saved")
	}
	app.Press("id:permission-never")
	if h.permission != "never" {
		t.Fatalf("permission = %q", h.permission)
	}
	app.Post(func() { h.permission = "on-failure"; uiruntime.Invalidate() })
	app.Settle()
	if !app.Find("id:permission-on-failure").Props.Checked {
		t.Fatal("permission did not follow controller")
	}
	app.Press("id:appearance")
	app.Press("id:appearance-2")
	if prefs.Load("appearance") != "dark" {
		t.Fatal("appearance not saved")
	}
	app.Press("id:interface-language")
	app.Press("id:interface-language-1")
	if prefs.Load("interfaceLanguage") != "zh-Hans" || locales.Current() != "zh-Hans" {
		t.Fatalf("language not applied: %q %q", prefs.Load("interfaceLanguage"), locales.Current())
	}
	if !strings.Contains(strings.Join(app.Texts(), "\n"), locales.TextIn("zh-Hans", "General")) {
		t.Fatal("language did not update category text")
	}
}
func TestSettingsAgentDraftSurvivesControllerUpdates(t *testing.T) {
	h := settingsHost()
	saved := "original"
	h.settings = []agent.Setting{{ID: "command", Title: "Command", Value: func() string { return saved }, Save: func(v string) { saved = v; uiruntime.Invalidate() }}}
	app, _ := startSettings(t, h, true)
	app.Clear("id:agent-setting-command")
	app.Type("id:agent-setting-command", "replacement")
	app.Post(func() { h.agents[0].Path = "/new/first"; uiruntime.Invalidate() })
	app.Settle()
	if got := app.Find("id:agent-setting-command").Text; got != "replacement" {
		t.Fatalf("draft reset to %q", got)
	}
	app.Press("id:settings-nav-phone")
	app.Press("id:settings-nav-general")
	if got := app.Find("id:agent-setting-command").Text; got != "replacement" {
		t.Fatalf("category switch lost draft: %q", got)
	}
	app.Press("id:save-setting-command")
	if saved != "replacement" {
		t.Fatalf("saved %q", saved)
	}
}
func TestSettingsPhoneActionsAndAccountRefresh(t *testing.T) {
	h := settingsHost()
	h.phoneOK = true
	app, _ := startSettings(t, h, true)
	app.Press("id:settings-nav-account")
	if h.refreshes != 1 {
		t.Fatalf("account refreshed %d times", h.refreshes)
	}
	app.Post(uiruntime.Invalidate)
	app.Settle()
	if h.refreshes != 1 {
		t.Fatalf("unrelated invalidation refreshed account again (%d)", h.refreshes)
	}
	app.Press("id:settings-nav-phone")
	app.Press("id:phone-start")
	if len(h.phoneActions) != 1 || h.phoneActions[0] != "start" {
		t.Fatalf("phone actions %v", h.phoneActions)
	}
	app.Post(func() { h.phone.Link = "miya://pair/example"; h.phone.Peers = 2; uiruntime.Invalidate() })
	app.Settle()
	if !app.Exists("id:phone-qr") {
		t.Fatal("missing QR after enabling phone")
	}
	app.Press("id:phone-copy")
	if app.Clipboard != "miya://pair/example" {
		t.Fatalf("clipboard = %q", app.Clipboard)
	}
	app.Press("id:phone-reset")
	app.Press("id:phone-stop")
	if strings.Join(h.phoneActions, ",") != "start,reset,stop" {
		t.Fatalf("phone actions %v", h.phoneActions)
	}
}
func TestSettingsMemoryUnavailableDoesNotPanic(t *testing.T) {
	h := settingsHost()
	app, _ := startSettings(t, h, true)
	app.Press("id:settings-nav-memory")
	app.WaitUntil(time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "Memory requires the Miya background service")
	})
	app.Post(func() { h.state.Cwd = "/"; uiruntime.Invalidate() })
	app.Settle()
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Open a project first.") {
		t.Fatal("root project guard missing")
	}
}

// These tests use an in-memory service, never the installed Miya process.
func settingsTestService(t *testing.T, handle func(svcproto.Request) (any, error)) *svcclient.Client {
	t.Helper()
	client, server := net.Pipe()
	cl := svcclient.NewClient(client)
	t.Cleanup(func() { cl.Close(); server.Close() })
	go func() {
		decoder, encoder := json.NewDecoder(server), json.NewEncoder(server)
		for {
			var req svcproto.Request
			if decoder.Decode(&req) != nil {
				return
			}
			result, err := handle(req)
			reply := svcproto.Envelope{ID: req.ID}
			if err != nil {
				reply.Error = err.Error()
			} else {
				reply.Result, _ = json.Marshal(result)
			}
			if encoder.Encode(reply) != nil {
				return
			}
		}
	}()
	return cl
}
func TestSettingsLoginCancelsDelayedStartAfterClose(t *testing.T) {
	h := settingsHost()
	started, release := make(chan struct{}), make(chan struct{})
	var cancelled atomic.Bool
	h.loginCall = func(r login.Request) (login.State, error) {
		switch r.Action {
		case "start":
			close(started)
			<-release
			return login.State{SessionID: "late-session", Phase: "waiting"}, nil
		case "cancel":
			if r.SessionID == "late-session" {
				cancelled.Store(true)
			}
		}
		return login.State{}, nil
	}
	app, _ := startSettings(t, h, true)
	app.Press("id:settings-nav-account")
	app.Press("id:login-start-codex")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("login was not started")
	}
	app.Press("id:settings-close")
	close(release)
	app.WaitUntil(time.Second, cancelled.Load)
}
func TestSettingsMemoryToggleUsesService(t *testing.T) {
	h := settingsHost()
	var disabled atomic.Bool
	h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) {
		switch req.Op {
		case "memoryList":
			return manager.List{Settings: memory.Settings{Disabled: disabled.Load()}}, nil
		case "memoryConfigure":
			var r manager.Request
			_ = json.Unmarshal(req.Args, &r)
			if r.Settings == nil {
				return nil, fmt.Errorf("missing settings")
			}
			disabled.Store(r.Settings.Disabled)
			return nil, nil
		}
		return nil, nil
	})
	app, _ := startSettings(t, h, true)
	app.Press("id:settings-nav-memory")
	app.WaitUntil(time.Second, func() bool { v := app.Find("id:memory-enabled"); return !v.Props.Disabled && v.Props.Checked })
	app.Press("id:memory-enabled")
	app.WaitUntil(time.Second, disabled.Load)
	app.WaitUntil(time.Second, func() bool { return !app.Find("id:memory-enabled").Props.Checked })
}
func TestSettingsRestartWaitsForIdleAndVerifiesNewProcess(t *testing.T) {
	h := settingsHost()
	var scheduled atomic.Bool
	h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) {
		switch req.Op {
		case "hello":
			return svcproto.Hello{PID: 100, Busy: true}, nil
		case "quitWhenIdle":
			scheduled.Store(true)
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected operation %s", req.Op)
		}
	})
	app, _ := startSettings(t, h, true)
	app.Press("id:restart-server")
	app.WaitFor("id:restart-idle", time.Second)
	if scheduled.Load() {
		t.Fatal("busy service restarted without choosing when")
	}
	app.Press("id:restart-idle")
	app.WaitUntil(time.Second, scheduled.Load)
	app.WaitUntil(time.Second, func() bool { return strings.Contains(strings.Join(app.Texts(), "\n"), "Restart scheduled.") })
	app.Press("id:settings-close")
	next := settingsTestService(t, func(req svcproto.Request) (any, error) { return svcproto.Hello{PID: 101}, nil })
	app.Post(func() { h.state.Remote = next; uiruntime.Invalidate() })
	app.Settle()
	app.Press("id:open-settings")
	app.WaitUntil(time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "Background service restarted and reconnected.")
	})
}

func TestSettingsMemoryEditorRequiresPreviewAndDeleteConfirmation(t *testing.T) {
	h := settingsHost()
	var entries []memory.Entry
	var promoted, deleted atomic.Bool
	h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) {
		var r manager.Request
		_ = json.Unmarshal(req.Args, &r)
		switch req.Op {
		case "memoryList":
			return manager.List{Entries: entries}, nil
		case "memorySave":
			entries = []memory.Entry{{ID: "one", Version: 1, Kind: r.Kind, Content: r.Content, State: "active", Scope: memory.Scope{Kind: "project"}}}
			return entries[0], nil
		case "memoryPromotionPreview":
			return manager.Promotion{File: r.File, Before: "Before", After: r.Content}, nil
		case "memoryPromoteFile":
			if r.Preview == "" {
				return nil, fmt.Errorf("missing approved preview")
			}
			promoted.Store(true)
			entries[0].State = "promoted"
			return nil, nil
		case "memoryDelete":
			deleted.Store(true)
			entries = nil
			return nil, nil
		}
		return nil, nil
	})
	app, _ := startSettings(t, h, true)
	app.Press("id:settings-nav-memory")
	app.WaitUntil(time.Second, func() bool { return !app.Find("id:memory-add").Props.Disabled })
	app.Press("id:memory-add")
	app.Type("id:memory-content", "Remember this fact")
	app.Press("id:memory-save")
	app.WaitFor("id:memory-entry-one", time.Second)
	app.WaitUntil(time.Second, func() bool { return !app.Find("id:memory-promote-file-one").Props.Disabled })
	app.Press("id:memory-promote-file-one")
	app.Clear("id:memory-promotion-content")
	app.Type("id:memory-promotion-content", "Use this wording")
	if got := app.Find("id:memory-promotion-content").Text; got != "Use this wording" {
		t.Fatalf("promotion input lost characters: %q", got)
	}
	if app.Exists("id:memory-promotion-confirm") {
		t.Fatal("confirmation shown before preview")
	}
	app.Press("id:memory-promotion-preview")
	app.WaitFor("id:memory-promotion-confirm", time.Second)
	app.Type("id:memory-promotion-content", " revised")
	if app.Exists("id:memory-promotion-confirm") {
		t.Fatal("stale preview can still be confirmed")
	}
	app.WaitUntil(time.Second, func() bool { return !app.Find("id:memory-promotion-preview").Props.Disabled })
	app.Press("id:memory-promotion-preview")
	app.WaitFor("id:memory-promotion-confirm", time.Second)
	app.WaitUntil(time.Second, func() bool { return !app.Find("id:memory-promotion-confirm").Props.Disabled })
	app.Press("id:memory-promotion-confirm")
	app.WaitUntil(time.Second, promoted.Load)
	app.WaitUntil(time.Second, func() bool { return !app.Find("id:memory-delete-one").Props.Disabled })
	app.Press("id:memory-delete-one")
	if deleted.Load() {
		t.Fatal("delete ran without second confirmation")
	}
	app.Press("id:memory-delete-confirm-one")
	app.WaitUntil(time.Second, deleted.Load)
	app.WaitForGone("id:memory-entry-one", time.Second)
}
