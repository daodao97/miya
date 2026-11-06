package integration

import (
	"testing"

	"miya/internal/prefs"
	"miya/internal/widgets"
)

func TestDesktopDarkMode(t *testing.T) {
	app, tt := testApp(t)
	t.Cleanup(func() { widgets.ApplyDesktopPalette(false) })
	if app.Dark || widgets.Canvas != widgets.DesktopLight.Canvas {
		t.Fatal("not light by default")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatalf("no appearance setting in %q: %v", tt.Texts(), err)
	}
	tt.Frame()
	if !app.Dark || !app.Theme.Dark || widgets.Canvas != widgets.DesktopDark.Canvas || widgets.Ink != widgets.DesktopDark.Ink || prefs.Load("appearance") != "dark" {
		t.Fatalf("dark not applied: dark %v setting %q", app.Dark, prefs.Load("appearance"))
	}
	widgets.SystemDark.Store(false)
	if err := tt.Click("System"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Dark || widgets.Canvas != widgets.DesktopLight.Canvas || prefs.Load("appearance") != "" {
		t.Fatal("system appearance not followed")
	}
	widgets.SystemDark.Store(true)
	t.Cleanup(func() { widgets.SystemDark.Store(false) })
	tt.Frame()
	if !app.Dark {
		t.Fatal("a dark system did not switch the palette")
	}
}
