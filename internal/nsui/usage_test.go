package nsui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/agent/account"
	"miya/internal/agent/quota"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/prefs"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
	"miya/locales"
)

type usageProvider struct {
	read  func(context.Context) (quota.Reading, error)
	reset func(context.Context, quota.ResetAttempt) (quota.ResetResult, error)
}

func (p usageProvider) ID() string                                      { return "codex" }
func (p usageProvider) Read(ctx context.Context) (quota.Reading, error) { return p.read(ctx) }
func (p usageProvider) Reset(ctx context.Context, a quota.ResetAttempt) (quota.ResetResult, error) {
	return p.reset(ctx, a)
}
func setUsageProvider(t *testing.T, p usageProvider) {
	t.Helper()
	before := environment
	environment.Quotas = func() *quota.Service { return quota.NewService(p) }
	t.Cleanup(func() { environment = before })
}
func usageFixture() quota.Reading {
	at := time.Now()
	return quota.Reading{Agent: "codex", Plan: "Plus", Account: "person@example.com", AccountKey: "account-1", Home: "/codex", At: at, Windows: []quota.Window{{Name: "5-hour", Used: 86, DurationMins: 300, ResetsAt: at.Add(2 * time.Hour).Unix(), Reset: "17:30"}}, Resets: &account.ResetCreditsSummary{AvailableCount: 1, Credits: []account.ResetCredit{{ID: "card-1", Status: "available"}}}}
}
func startUsagePanel(t *testing.T, r quota.Reading) (*nsgotest.App, *desktopUsage) {
	t.Helper()
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	locales.SetCurrent("en")
	t.Cleanup(func() { locales.SetCurrent("") })
	var d *desktopUsage
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		d = newDesktopUsage(cx, newFakeHost(model.Thread{ID: 1}))
		d.active = true
		d.readings = []quota.Reading{r}
		d.panelAgent = "codex"
		return d.panelView(cx)
	}, nsgotest.Size(usagePanelWidth, usagePanelLimit))
	return app, d
}
func TestUsagePanelSwitchRefreshAndResetTimes(t *testing.T) {
	var calls atomic.Int32
	setUsageProvider(t, usageProvider{read: func(context.Context) (quota.Reading, error) {
		calls.Add(1)
		r := usageFixture()
		r.Windows[0].Used = 25
		return r, nil
	}})
	app, d := startUsagePanel(t, usageFixture())
	text := strings.Join(app.Texts(), "\n")
	for _, part := range []string{"86%", "17:30 reset", "Steady pace", "1 reset cards", "Plus"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %q in %s", part, text)
		}
	}
	if !app.Exists("id:usage-window-codex-0-time") {
		t.Fatal("cycle time marker missing")
	}
	panel := app.Find("id:usage-panel").Bounds()
	if panel.Size.Width != usagePanelWidth || panel.Size.Height >= usagePanelLimit {
		t.Fatalf("usage panel did not fit the active reading: %v", panel)
	}
	pace, reset := app.Find("id:usage-window-codex-0-pace").Bounds(), app.Find("id:usage-window-codex-0-reset").Bounds()
	if pace.Origin.Y != reset.Origin.Y || pace.Origin.X+pace.Size.Width > reset.Origin.X {
		t.Fatalf("pace and reset should share one row: %v and %v", pace, reset)
	}
	if !app.Find("id:usage-tab-codex").Props.Selected || app.Find("id:usage-tab-claude").Props.Selected {
		t.Fatal("wrong selected tab")
	}
	app.Press("id:usage-tab-claude")
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "No usage data") {
		t.Fatal("empty account missing")
	}
	if d.agent != "" {
		t.Fatal("preview changed tray preference")
	}
	if got := app.Find("id:usage-panel").Bounds().Size.Height; got >= panel.Size.Height || !app.Find("id:usage-tab-claude").Props.Selected {
		t.Fatalf("panel did not resize or select the new tab: height %v", got)
	}
	app.Press("id:usage-tab-codex")
	app.Press("id:usage-refresh")
	app.WaitUntil(time.Second, func() bool { return !d.busy })
	if calls.Load() != 1 || !strings.Contains(strings.Join(app.Texts(), "\n"), "25%") {
		t.Fatal("refresh did not update panel")
	}
}
func TestUsagePanelErrorLoadingAndMinuteTick(t *testing.T) {
	r := quota.Reading{Agent: "codex", Error: "CLI unavailable"}
	app, d := startUsagePanel(t, r)
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "CLI unavailable") {
		t.Fatal("error missing")
	}
	app.Post(func() { d.busy = true; d.changed() })
	app.Settle()
	if app.Enabled("id:usage-refresh") {
		t.Fatal("refresh enabled during read")
	}
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Checking Codex account.") {
		t.Fatal("loading state missing")
	}
	app.Post(func() {
		d.busy = false
		d.readings = []quota.Reading{usageFixture()}
		d.readings[0].At = app.Clock.Now()
		d.changed()
	})
	app.Settle()
	if !strings.Contains(app.Find("id:usage-status").Text, "just now") {
		t.Fatal("fresh status missing")
	}
	app.FastForward(2 * time.Minute)
	if strings.Contains(app.Find("id:usage-status").Text, "just now") {
		t.Fatal("status did not age without polling")
	}
}
func TestUsageResetRequiresConfirmationAndReusesOperation(t *testing.T) {
	var mu sync.Mutex
	var attempts []quota.ResetAttempt
	setUsageProvider(t, usageProvider{reset: func(_ context.Context, a quota.ResetAttempt) (quota.ResetResult, error) {
		mu.Lock()
		defer mu.Unlock()
		attempts = append(attempts, a)
		if len(attempts) == 1 {
			return quota.ResetResult{}, errors.New("connection lost")
		}
		r := usageFixture()
		r.Resets.AvailableCount = 0
		return quota.ResetResult{Outcome: "reset", Reading: r}, nil
	}})
	app, d := startUsagePanel(t, usageFixture())
	app.Press("id:usage-reset-codex")
	mu.Lock()
	n := len(attempts)
	mu.Unlock()
	if n != 0 {
		t.Fatal("card used before confirmation")
	}
	app.Press("id:usage-reset-cancel")
	app.Press("id:usage-reset-codex")
	app.Press("id:usage-reset-confirm")
	app.WaitUntil(time.Second, func() bool { return !d.busy })
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "connection lost") {
		t.Fatal("ambiguous result missing")
	}
	app.Press("id:usage-reset-confirm")
	app.WaitUntil(time.Second, func() bool { return !d.busy })
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 || attempts[0] != attempts[1] || attempts[0].Key == "" {
		t.Fatalf("retry changed operation: %+v", attempts)
	}
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Used 1 reset card. Quota updated.") {
		t.Fatal("reset outcome missing")
	}
}
func TestUsageSettingsPreferences(t *testing.T) {
	app, _ := startSettings(t, settingsHost(), true)
	app.Press("id:settings-nav-usage")
	app.WaitFor("id:usage-tray", time.Second)
	if !app.Exists("id:usage-card-codex") || !app.Exists("id:usage-card-claude") {
		t.Fatal("subscription cards missing")
	}
	app.Press("id:usage-tray")
	if prefs.Load("usageTray") != "off" {
		t.Fatal("visibility preference not saved")
	}
	app.Press("id:usage-agent")
	app.Press("id:usage-agent-2")
	if prefs.Load("usageTrayAgent") != "claude" {
		t.Fatal("agent preference not saved")
	}
	app.Press("id:usage-icon")
	app.Press("id:usage-icon-1")
	if prefs.Load("usageTrayIcon") != "agent" {
		t.Fatal("icon preference not saved")
	}
	app.Press("id:settings-nav-account")
	app.Press("id:settings-nav-usage")
	if app.Find("id:usage-tray").Props.Checked {
		t.Fatal("category switch lost visibility")
	}
}
func TestUsagePollingStopsOnWorkspaceSwitchAndDropsLateRead(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	started, cancelled, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	setUsageProvider(t, usageProvider{read: func(ctx context.Context) (quota.Reading, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			defer close(done)
		}
		return usageFixture(), nil
	}})
	app, hub := startHub(t, nil)
	app.WaitUntil(time.Second, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	old := hub.usage
	h := newFakeHost(model.Thread{ID: 2})
	app.Post(func() { hub.activate(h) })
	app.WaitUntil(time.Second, func() bool {
		select {
		case <-cancelled:
			return true
		default:
			return false
		}
	})
	close(release)
	<-done
	app.Settle()
	if old.active || old.busy || len(old.readings) != 0 {
		t.Fatal("inactive workspace accepted late response")
	}
	app.WaitUntil(time.Second, func() bool { return !hub.usage.busy })
	count := calls.Load()
	app.FastForward(3 * time.Minute)
	app.WaitUntil(time.Second, func() bool { return calls.Load() > count && !hub.usage.busy })
	app.Post(func() {
		prefs.Save("usageTray", "off")
		prefs.Save("usageTrayAgent", "claude")
		hub.activate(hub.root)
	})
	app.Settle()
	if hub.usage != old || !old.hidden || old.agent != "claude" {
		t.Fatal("returning workspace did not reload device preferences")
	}
	app.WaitUntil(time.Second, func() bool { return !hub.usage.busy })
	app.Post(hub.close)
	app.Settle()
	count = calls.Load()
	app.FastForward(6 * time.Minute)
	if calls.Load() != count {
		t.Fatal("polling continued after stop")
	}
}
func TestUsageIconsArePNG(t *testing.T) {
	for _, agent := range append(usageAgents(), "", "unsupported") {
		if _, err := png.DecodeConfig(bytes.NewReader(UsageTrayIconFor(agent))); err != nil {
			t.Fatalf("%s icon: %v", agent, err)
		}
	}
}

// Invalidation still updates connected-account controls alongside the shared
// standalone readings; the tray never needs controller implementation imports.
func TestUsageOpensSettingsCategoryFromSettings(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	locales.SetCurrent("en")
	t.Cleanup(func() { locales.SetCurrent(""); uiruntime.SetAppWindow(nil); current = nil })
	app := startBridged(t, settingsHost())
	app.Press("Settings")
	app.Press("Usage")
	app.WaitFor("id:usage-tray", time.Second)
}

func TestUsageRemoteReadsAndResetsUseSelectedHost(t *testing.T) {
	var reads, resets, local atomic.Int32
	setUsageProvider(t, usageProvider{read: func(context.Context) (quota.Reading, error) { local.Add(1); return usageFixture(), nil }})
	h := settingsHost()
	h.remote = true
	h.state.Remote = settingsTestService(t, func(req svcproto.Request) (any, error) {
		if req.Op != "host" {
			return nil, nil
		}
		var request svcproto.HostRequest
		if err := json.Unmarshal(req.Args, &request); err != nil {
			return nil, err
		}
		switch request.Action {
		case "usage":
			reads.Add(1)
			return []quota.Reading{usageFixture()}, nil
		case "usage-reset":
			if request.Reset == nil || request.Reset.AccountKey != "account-1" || request.Reset.Home != "/codex" {
				return nil, errors.New("wrong confirmed account")
			}
			resets.Add(1)
			return quota.ResetResult{Outcome: "alreadyRedeemed", Reading: usageFixture()}, nil
		}
		return nil, nil
	})
	app, _ := startSettings(t, h, true)
	app.Press("id:settings-nav-usage")
	app.WaitUntil(time.Second, func() bool {
		return reads.Load() > 0 && app.Exists("id:usage-reset-codex") && app.Enabled("id:usage-reset-codex")
	})
	app.Press("id:usage-reset-codex")
	app.Press("id:usage-reset-confirm")
	app.WaitUntil(time.Second, func() bool {
		return resets.Load() == 1 && strings.Contains(strings.Join(app.Texts(), "\n"), "The last reset completed. No additional card used.")
	})
	if local.Load() != 0 {
		t.Fatal("remote workspace read local CLI")
	}
}
func TestUsageInterruptedResetKeepsRetryKey(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	setUsageProvider(t, usageProvider{reset: func(ctx context.Context, _ quota.ResetAttempt) (quota.ResetResult, error) {
		close(started)
		<-ctx.Done()
		<-release
		defer close(done)
		return quota.ResetResult{}, ctx.Err()
	}})
	app, d := startUsagePanel(t, usageFixture())
	app.Press("id:usage-reset-codex")
	key := d.reset.Key
	app.Press("id:usage-reset-confirm")
	app.WaitUntil(time.Second, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	app.Post(d.stop)
	app.Settle()
	close(release)
	<-done
	app.Settle()
	if !d.resetRetry || d.reset.Key != key {
		t.Fatal("interrupted operation key lost")
	}
	app.Post(func() { d.active = true; d.beginReset(usageFixture()) })
	app.Settle()
	if d.reset.Key != key {
		t.Fatal("returning to workspace selected another operation")
	}
}
