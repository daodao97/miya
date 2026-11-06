package integration

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/prefs"
	"miya/internal/widgets"
	"miya/locales"
)

func TestInterfaceLanguageSystemMatching(t *testing.T) {
	for _, test := range []struct{ preferred, want string }{{"zh-CN", "zh-Hans"}, {"en-GB", "en"}, {"de-DE", "en"}} {
		got := i18n.Match([]string{test.preferred}, locales.Catalog.Languages())
		if got == "" {
			got = locales.Catalog.Source()
		}
		if got != test.want {
			t.Errorf("system %s matched %s, want %s", test.preferred, got, test.want)
		}
	}
	app, tt := testApp(t)
	t.Cleanup(func() { locales.SetCurrent("en") })
	if got := prefs.Load("interfaceLanguage"); got != "" {
		t.Fatalf("fresh installation has an override: %q", got)
	}
	tt.SetLocale("zh-Hans")
	if !tt.HasText("新建对话") || !tt.HasText("自动化") || !tt.HasText("项目") {
		t.Fatalf("Chinese system UI not applied: %q", tt.Texts())
	}
	app.Section = "Settings"
	tt.Frame()
	for _, label := range []string{"通用", "界面语言", "跟随系统", "English", "简体中文"} {
		if !tt.HasText(label) {
			t.Fatalf("missing localized setting %q: %q", label, tt.Texts())
		}
	}
	saveReconnectSnapshot(t, tt, "language-zh-settings")
	tt.SetLocale("en")
	if !tt.HasText("General") || !tt.HasText("Follow system") || app.Section != "Settings" {
		t.Fatal("system language change lost the current page or failed to redraw")
	}
	saveReconnectSnapshot(t, tt, "language-en-settings")
}

func TestInterfaceLanguageChoiceAndDeviceIsolation(t *testing.T) {
	app, tt := testApp(t)
	t.Cleanup(func() { locales.SetCurrent("en") })
	app.C.(*controller.Controller).RememberDraft("Keep my draft")
	app.Section = "Settings"
	tt.Frame()
	oldApply := widgets.ApplyInterfaceLanguage
	var chosen []string
	widgets.ApplyInterfaceLanguage = func(tag string) { chosen = append(chosen, tag) }
	t.Cleanup(func() { widgets.ApplyInterfaceLanguage = oldApply })
	if err := tt.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	if len(chosen) != 1 || chosen[0] != "zh-Hans" || prefs.Load("interfaceLanguage") != "zh-Hans" {
		t.Fatalf("language was not applied and saved locally: %v", chosen)
	}
	tt.SetLocale(chosen[0])
	mirror := controller.NewMirrorController()
	t.Cleanup(mirror.Cancel)
	mirror.Mirror.SetLocale("en")
	app.C.(*controller.Controller).SetLocale("en")
	tt.Frame()
	if !tt.HasText("界面语言") || app.C.(*controller.Controller).Draft() != "Keep my draft" {
		t.Fatal("service locale replaced the device language or changed a draft")
	}
	if err := tt.Click("跟随系统"); err != nil {
		t.Fatal(err)
	}
	if len(chosen) != 2 || chosen[1] != "" || prefs.Load("interfaceLanguage") != "system" {
		t.Fatal("following system did not clear the MyGo override")
	}
	widgets.ConfigureInterfaceLanguage()
	if len(chosen) != 3 || chosen[2] != "" {
		t.Fatal("saved system preference was not restored on startup")
	}
}

func TestInterfaceLanguagePaletteAndUserContent(t *testing.T) {
	app, tt := testApp(t)
	t.Cleanup(func() { locales.SetCurrent("en") })
	app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, Title: "Settings", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "user", Content: "New thread"}}}})
	app.C.(*controller.Controller).SelectThread(app.C.(*controller.Controller).ThreadWithID(1))
	tt.SetLocale("zh-Hans")
	if !tt.HasText("Settings") || !tt.HasText("New thread") {
		t.Fatal("user title or message was translated")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Type("settings")
	if !tt.HasText("打开设置") {
		t.Fatalf("English command search failed in Chinese UI: %q", tt.Texts())
	}
	saveReconnectSnapshot(t, tt, "language-zh-palette")
	tt.Key(0, ui.KeyEnter)
	if app.Section != "Settings" || !tt.HasText("通用") {
		t.Fatal("localized command changed a navigation identifier")
	}
}

func TestMobileLanguageChoiceFitsNarrowScreen(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Cleanup(func() { locales.SetCurrent("en") })
			m := testMobileApp()
			m.Router().Push("/settings")
			tt := ui.NewTester(m.ViewRoot, width, 844)
			tt.SetLocale("zh-Hans")
			for _, label := range []string{"跟随系统", "简体中文", "English"} {
				rect, ok := tt.Find(label)
				if !ok || rect.W <= 0 || rect.X < 0 || rect.X+rect.W > float32(width) {
					t.Fatalf("language option %q overflows: %v", label, rect)
				}
			}
			saveReconnectSnapshot(t, tt, fmt.Sprintf("language-mobile-%d", width))
		})
	}
}
