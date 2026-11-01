package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/agentusage"
)

func usageFixture() *chatApp {
	return &chatApp{usage: &desktopUsage{prefs: usagePreferences{agent: "codex"}, panelAgent: "codex", readings: []agentusage.Reading{
		{Agent: "codex", Account: "test@example.com", AccountKey: "test", Home: "/test/codex", Plan: "pro", At: time.Now(), Windows: []agentusage.Window{{Name: "5 小时", Used: 17, Reset: "11-01 19:00"}, {Name: "7 天", Used: 92, Reset: "11-07 09:00"}}},
		{Agent: "claude", At: time.Now(), Error: "请先登录 Claude CLI"},
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
	for _, label := range []string{"查看 Codex 用量", "查看 Claude 用量", "用量设置", "刷新用量"} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > usagePanelWidth || r.Y+r.H > float32(height) {
			t.Fatalf("panel control %q is unreachable: %v", label, r)
		}
	}
}

func TestUsagePanelProviderPreviewAndThemes(t *testing.T) {
	app := usageFixture()
	tt := ui.NewTester(app.usagePanelView, usagePanelWidth, app.usage.height())
	for _, text := range []string{"剩余 83%", "剩余 8%", "11-01 19:00 重置", "pro"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
	assertUsageControls(t, tt, app.usage.height())
	saveUsageImage(t, tt, "usage-light")
	tt.SetDark(true)
	saveUsageImage(t, tt, "usage-dark")
	if err := tt.Click("查看 Claude 用量"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(usagePanelWidth, app.usage.height())
	if app.usage.panelAgent != "claude" || app.usage.prefs.agent != "codex" || !tt.HasText("请先登录 Claude CLI") || tt.HasText("剩余 83%") {
		t.Fatalf("provider preview changed the tray source or showed wrong data: %q", tt.Texts())
	}
	assertUsageControls(t, tt, app.usage.height())
	saveUsageImage(t, tt, "usage-error")
	tt.Key(0, ui.KeyLeft)
	if app.usage.panelAgent != "codex" {
		t.Fatal("provider tabs do not support the keyboard")
	}
}

func TestUsagePanelScrollKeepsControlsAvailable(t *testing.T) {
	app := usageFixture()
	r := &app.usage.readings[0]
	r.Windows = append(r.Windows, agentusage.Window{Name: "额外周期", Used: 100}, agentusage.Window{Name: "另一个周期", Used: 0, Reset: "11-08 08:00"})
	tt := ui.NewTester(app.usagePanelView, usagePanelWidth, app.usage.height())
	tt.Scroll(160, 150, 0, 400)
	last, ok := tt.Find("11-08 08:00 重置")
	if !ok || last.Y < usagePanelHeader || last.Y+last.H > float32(app.usage.height()-usagePanelFooter) {
		t.Fatalf("extra periods cannot be read by scrolling: %v", last)
	}
	assertUsageControls(t, tt, app.usage.height())
	saveUsageImage(t, tt, "usage-scrolled")
	r.Windows = nil
	r.Error = strings.Repeat("本机账号尚未登录，请先在 CLI 登录后刷新。", 12)
	tt.SetSize(usagePanelWidth, app.usage.height())
	tt.Scroll(160, 100, 0, 500)
	assertUsageControls(t, tt, app.usage.height())
	saveUsageImage(t, tt, "usage-long-error")
}

func TestUsagePanelResetRequiresConfirmation(t *testing.T) {
	app := usageFixture()
	expires := time.Now().Add(24 * time.Hour).Unix()
	app.usage.readings[0].Resets = &agentusage.ResetCredits{Count: 3, Credits: []agentusage.ResetCard{{ID: "known", Type: "codexRateLimits", Status: "available", ExpiresAt: &expires}}}
	tt := ui.NewTester(app.usagePanelView, usagePanelWidth, app.usage.height())
	if !tt.HasText("3 张重置卡") || !tt.HasText("已提供 1 / 3 张卡的到期信息") {
		t.Fatalf("partial reset details are misrepresented: %q", tt.Texts())
	}
	saveUsageImage(t, tt, "usage-reset-cards")
	if err := tt.Click("Use Codex reset"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(usagePanelWidth, app.usage.height())
	d := app.usage
	if !d.resetOpen || d.resetAttempt == nil || d.resetAttempt.CreditID != "" || d.resetting || d.busy || !tt.HasText("确认使用") {
		t.Fatal("using a card skipped confirmation or chose from incomplete details")
	}
	assertUsageControls(t, tt, app.usage.height())
	saveUsageImage(t, tt, "usage-reset-confirm")
	if err := tt.Click("Cancel Codex reset"); err != nil {
		t.Fatal(err)
	}
	if d.resetOpen || d.busy || d.resetting {
		t.Fatal("cancel submitted a reset")
	}
}

func TestUsageSettingsPersistWithoutChangingConversation(t *testing.T) {
	app, tt := testApp(t)
	app.ensureUsage()
	app.section, app.settingsTab = "Settings", 3
	tt.Frame()
	if err := tt.Click("显示菜单栏用量"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Claude"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Agent"); err != nil {
		t.Fatal(err)
	}
	p := loadUsagePreferences()
	if !p.hidden || p.agent != "claude" || !p.agentIcon || app.c.active() != 0 {
		t.Fatalf("usage preferences were not saved independently: %+v", p)
	}
}

func TestUsageTraySelectionAndNativeTitle(t *testing.T) {
	readings := []agentusage.Reading{{Agent: "claude", Windows: []agentusage.Window{{Used: 40}}}, {Agent: "codex", Windows: []agentusage.Window{{Used: 0}, {Used: 100}}}}
	if r := selectedUsage(readings, ""); r == nil || r.Agent != "codex" || usageTrayTitle(r) != "100% · 0%" {
		t.Fatal("auto selection does not prefer Codex or inverts remaining quota")
	}
	readings[1].Error = "not available"
	if selectedUsage(readings, "codex") != nil || usageTrayTitle(nil) != "—" || selectedUsage(readings, "").Agent != "claude" {
		t.Fatal("missing fixed account fell back or was shown as zero")
	}
	for _, agent := range []string{"", "codex", "claude"} {
		img, err := png.Decode(bytes.NewReader(usageTrayIconFor(agent)))
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
		{want: "尚未更新"}, {busy: true, want: "更新中"},
		{r: agentusage.Reading{At: now, Error: "failed"}, want: "刚刚检查"},
		{r: agentusage.Reading{At: now, Windows: []agentusage.Window{{Used: 0}}}, want: "刚刚更新"},
	} {
		if got := usagePanelStatus(tc.r, tc.busy, now); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}
