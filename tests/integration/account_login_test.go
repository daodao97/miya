package integration

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent/login"
	desktopui "miya/internal/ui"
	"miya/internal/widgets"
	"miya/locales"
)

func TestNativeLoginCardsShowProviderSpecificCodeDirection(t *testing.T) {
	t.Cleanup(func() { locales.SetCurrent("en") })
	t.Cleanup(func() { widgets.ApplyDesktopPalette(false) })
	for _, language := range []string{"en", "zh-Hans"} {
		locales.SetCurrent(language)
		for _, dark := range []bool{false, true} {
			widgets.ApplyDesktopPalette(dark)
			codex := desktopui.AccountLoginUI{State: login.State{Agent: "codex", Method: "device", Phase: "waiting", Challenge: login.Challenge{URL: "https://auth.openai.com/codex/device", UserCode: "ABCD-1234"}}}
			claude := desktopui.AccountLoginUI{State: login.State{Agent: "claude", Method: "browser", Phase: "waiting", Challenge: login.Challenge{URL: "https://claude.ai/oauth/authorize?state=fixture-state"}}}
			last := ""
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().Padding(20).Gap(16).Children(func() {
					desktopui.NativeLoginCard(c.Key("codex"), "codex", &codex, func(action string) { last = "codex:" + action })
					desktopui.NativeLoginCard(c.Key("claude"), "claude", &claude, func(action string) { last = "claude:" + action })
				})
			}, 720, 750)
			tt.SetDark(dark)
			copyCode := locales.TextIn(language, "Copy device code")
			if err := tt.Click(copyCode); err != nil {
				t.Fatal(err)
			}
			if tt.Clipboard() != "ABCD-1234" {
				t.Fatal("device code not copied")
			}
			if _, ok := tt.Find(fmt.Sprintf(locales.TextIn(language, "%s authorization code"), "Claude Code")); !ok {
				t.Fatal("manual-code field missing")
			}
			if err := tt.Click(locales.TextIn(language, "Submit code")); err == nil && last != "" {
				t.Fatal("empty authorization code submitted")
			}
			if err := tt.Click(fmt.Sprintf(locales.TextIn(language, "Check %s sign-in"), "Codex")); err != nil {
				t.Fatal(err)
			}
			if last != "codex:check" {
				t.Fatal("status check went to wrong provider")
			}
			suffix := "light"
			if dark {
				suffix = "dark"
			}
			saveUsageImage(t, tt, "native-login-"+language+"-"+suffix)
		}
	}
}

func TestAccountTabHasLocalNativeLoginEntryPoints(t *testing.T) {
	app, tt := testApp(t)
	app.Section, app.SettingsTab = "Settings", 1
	tt.Frame()
	for _, label := range []string{"Accounts on this computer", "Sign in to Codex", "Sign in to Claude Code", "Check Codex sign-in", "Check Claude Code sign-in"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("missing account login entry %q in %q", label, tt.Texts())
		}
	}
}
