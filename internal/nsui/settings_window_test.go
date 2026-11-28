package nsui

import (
	"strconv"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/nswidgets"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
	"miya/locales"
)

func TestSettingsWindowSelectSurface(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	t.Cleanup(func() {
		uiruntime.SetAppWindow(nil)
		current = nil
		locales.SetCurrent("")
	})
	prefs.Save("interfaceLanguage", "zh-Hans")
	prefs.Save("appearance", "dark")
	h := settingsHost()
	app := nsgotest.StartApp(t, func(cx *core.Scope) {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		cx.SetLocale(prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage")))
		cx.SetMainView(func(cx *core.Scope) core.Node {
			return core.Dynamic(func(cx *core.Scope) core.Node {
				interfaceLanguage(cx)
				return core.Dynamic(func(cx *core.Scope) core.Node { return root(cx, h) })
			})
		})
	}, nsgotest.Size(890, 660), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}), nsgotest.Offscreen())

	app.Press("label:" + locales.TextIn("zh-Hans", "Settings"))
	settings := app.Surface("title:" + locales.TextIn("zh-Hans", "Settings"))
	want, _ := nswidgets.Theme().Dark.Color(nswidgets.RolePanel)
	for _, selectTest := range []struct {
		id       string
		selected int
	}{{"interface-language", 1}, {"appearance", 2}} {
		id := "id:" + selectTest.id
		option := id + "-" + strconv.Itoa(selectTest.selected)
		settings.Press(id)
		settings.WaitFor(option, time.Second)
		if nsgotest.Target() == "macos" {
			app.Advance(200 * time.Millisecond)
			settings.Screenshot(selectTest.id)
		}
		popup := settings.Find("role:listbox").Parent
		if popup.Style.Background != want {
			t.Errorf("%s popup background = %v, want the opaque panel color %v", selectTest.id, popup.Style.Background, want)
		}
		settings.Press(option)
	}

	settings.Press("id:appearance")
	settings.Press("id:appearance-1")
	settings.Press("id:interface-language")
	settings.WaitFor("id:interface-language-1", time.Second)
	light, _ := nswidgets.Theme().Light.Color(nswidgets.RolePanel)
	if got := settings.Find("role:listbox").Parent.Style.Background; got != light {
		t.Fatalf("popup background after switching to light appearance = %v, want %v", got, light)
	}
	if nsgotest.Target() == "macos" {
		settings.Screenshot("interface-language-light")
	}
}
