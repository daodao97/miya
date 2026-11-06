package integration

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent/account"
	agentusage "miya/internal/agent/quota"
	"miya/internal/controller"
	desktopui "miya/internal/ui"
)

func usageFixture() *desktopui.App {
	return &desktopui.App{Usage: &desktopui.DesktopUsage{Prefs: desktopui.UsagePreferences{Agent: "codex"}, PanelAgent: "codex", Readings: []agentusage.Reading{
		{Agent: "codex", Account: "test@example.com", AccountKey: "test", Home: "/test/codex", Plan: "pro", At: time.Now(), Windows: []agentusage.Window{{Name: "5 hours", Used: 17, Reset: "11-01 19:00"}, {Name: "7 days", Used: 92, Reset: "11-07 09:00"}}},
		{Agent: "claude", At: time.Now(), Error: "Sign in to Claude CLI first"},
	}}}
}

func saveUsageImage(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MIYA_SNAPSHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func assertUsageControls(t *testing.T, tt *ui.Tester, height int) {
	t.Helper()
	for _, label := range []string{"View Codex usage", "View Claude usage", "Usage settings", "Refresh usage"} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > desktopui.UsagePanelWidth || r.Y+r.H > float32(height) {
			t.Fatalf("panel control %q is unreachable: %v", label, r)
		}
	}
}

func TestUsagePanelProviderPreviewAndThemes(t *testing.T) {
	app := usageFixture()
	tt := ui.NewTester(app.UsagePanelView, desktopui.UsagePanelWidth, app.Usage.Height())
	for _, text := range []string{"17% used", "92% used", "11-01 19:00 reset", "pro"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
	assertUsageControls(t, tt, app.Usage.Height())
	saveUsageImage(t, tt, "usage-light")
	tt.SetDark(true)
	saveUsageImage(t, tt, "usage-dark")
	if err := tt.Click("View Claude usage"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(desktopui.UsagePanelWidth, app.Usage.Height())
	if app.Usage.PanelAgent != "claude" || app.Usage.Prefs.Agent != "codex" || !tt.HasText("Sign in to Claude CLI first") || tt.HasText("17% used") {
		t.Fatalf("provider preview changed the tray source or showed wrong data: %q", tt.Texts())
	}
	assertUsageControls(t, tt, app.Usage.Height())
	saveUsageImage(t, tt, "usage-error")
	tt.Key(0, ui.KeyLeft)
	if app.Usage.PanelAgent != "codex" {
		t.Fatal("provider tabs do not support the keyboard")
	}
}

func TestUsagePanelScrollKeepsControlsAvailable(t *testing.T) {
	app := usageFixture()
	r := &app.Usage.Readings[0]
	r.Windows = append(r.Windows, agentusage.Window{Name: "额外周期", Used: 100}, agentusage.Window{Name: "另一个周期", Used: 0, Reset: "11-08 08:00"})
	tt := ui.NewTester(app.UsagePanelView, desktopui.UsagePanelWidth, app.Usage.Height())
	tt.Scroll(160, 150, 0, 400)
	last, ok := tt.Find("11-08 08:00 reset")
	if !ok || last.Y < desktopui.UsagePanelHeader || last.Y+last.H > float32(app.Usage.Height()-desktopui.UsagePanelFooter) {
		t.Fatalf("extra periods cannot be read by scrolling: %v", last)
	}
	assertUsageControls(t, tt, app.Usage.Height())
	saveUsageImage(t, tt, "usage-scrolled")
	r.Windows = nil
	r.Error = strings.Repeat("本机账号尚未登录，请先在 CLI 登录后刷新。", 12)
	tt.SetSize(desktopui.UsagePanelWidth, app.Usage.Height())
	tt.Scroll(160, 100, 0, 500)
	assertUsageControls(t, tt, app.Usage.Height())
	saveUsageImage(t, tt, "usage-long-error")
}

func TestUsagePanelResetRequiresConfirmation(t *testing.T) {
	app := usageFixture()
	expires := time.Now().Add(24 * time.Hour).Unix()
	app.Usage.Readings[0].Resets = &account.ResetCreditsSummary{AvailableCount: 3, Credits: []account.ResetCredit{{ID: "known", ResetType: "codexRateLimits", Status: "available", ExpiresAt: &expires}}}
	tt := ui.NewTester(app.UsagePanelView, desktopui.UsagePanelWidth, app.Usage.Height())
	if !tt.HasText("3 reset cards") || !tt.HasText("Expiration available for 1 / 3 cards") {
		t.Fatalf("partial reset details are misrepresented: %q", tt.Texts())
	}
	saveUsageImage(t, tt, "usage-reset-cards")
	if err := tt.Click("Use Codex reset"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(desktopui.UsagePanelWidth, app.Usage.Height())
	d := app.Usage
	if !d.ResetOpen || d.ResetAttempt == nil || d.ResetAttempt.CreditID != "" || d.Resetting || d.Busy || !tt.HasText("Confirm") {
		t.Fatal("using a card skipped confirmation or chose from incomplete details")
	}
	assertUsageControls(t, tt, app.Usage.Height())
	saveUsageImage(t, tt, "usage-reset-confirm")
	if err := tt.Click("Cancel Codex reset"); err != nil {
		t.Fatal(err)
	}
	if d.ResetOpen || d.Busy || d.Resetting {
		t.Fatal("cancel submitted a reset")
	}
}

func TestUsageSettingsPersistWithoutChangingConversation(t *testing.T) {
	app, tt := testApp(t)
	app.EnsureUsage()
	app.Section, app.SettingsTab = "Settings", 3
	tt.Frame()
	if err := tt.Click("Show menu bar usage"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Claude"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Agent"); err != nil {
		t.Fatal(err)
	}
	p := desktopui.LoadUsagePreferences()
	if !p.Hidden || p.Agent != "claude" || !p.AgentIcon || app.C.(*controller.Controller).Active() != 0 {
		t.Fatalf("usage preferences were not saved independently: %+v", p)
	}
}

func TestUsageTraySelectionAndNativeTitle(t *testing.T) {
	readings := []agentusage.Reading{{Agent: "claude", Windows: []agentusage.Window{{Used: 40}}}, {Agent: "codex", Windows: []agentusage.Window{{Used: 0}, {Used: 100}}}}
	if r := agentusage.SelectedUsage(readings, ""); r == nil || r.Agent != "codex" || agentusage.TrayTitle(r) != "100% · 0%" {
		t.Fatal("auto selection does not prefer Codex or inverts remaining quota")
	}
	readings[1].Error = "not available"
	if agentusage.SelectedUsage(readings, "codex") != nil || agentusage.TrayTitle(nil) != "—" || agentusage.SelectedUsage(readings, "").Agent != "claude" {
		t.Fatal("missing fixed account fell back or was shown as zero")
	}
	for _, agent := range []string{"", "codex", "claude"} {
		img, err := png.Decode(bytes.NewReader(desktopui.UsageTrayIconFor(agent)))
		if err != nil || img.Bounds().Dx() != 36 || img.Bounds().Dy() != 36 {
			t.Fatalf("invalid %q tray icon: %v", agent, err)
		}
		for y := 0; y < 36; y++ {
			for x := 0; x < 36; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r != 0 || g != 0 || b != 0 {
					t.Fatal("tray template is not monochrome")
				}
			}
		}
	}
}

func TestUsageStatusDoesNotTreatFailureAsUpdate(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		r    agentusage.Reading
		busy bool
		want string
	}{
		{want: "Not updated yet"}, {busy: true, want: "Updating"},
		{r: agentusage.Reading{At: now, Error: "failed"}, want: "Checked just now"},
		{r: agentusage.Reading{At: now, Windows: []agentusage.Window{{Used: 0}}}, want: "Updated just now"},
	} {
		if got := desktopui.UsagePanelStatus(tc.r, tc.busy, now); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}
