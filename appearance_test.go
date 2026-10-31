package main

import "testing"

func TestDesktopDarkMode(t *testing.T) {
	app, tt := testApp(t)
	t.Cleanup(func() { applyDesktopPalette(false) })
	if app.dark || canvas != desktopLight.canvas {
		t.Fatal("not light by default")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatalf("no appearance setting in %q: %v", tt.Texts(), err)
	}
	tt.Frame()
	if !app.dark || !app.theme.Dark || canvas != desktopDark.canvas || ink != desktopDark.ink || loadSetting("appearance") != "dark" {
		t.Fatalf("dark not applied: dark %v setting %q", app.dark, loadSetting("appearance"))
	}
	systemDark.Store(false)
	if err := tt.Click("System"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.dark || canvas != desktopLight.canvas || loadSetting("appearance") != "" {
		t.Fatal("system appearance not followed")
	}
	systemDark.Store(true)
	t.Cleanup(func() { systemDark.Store(false) })
	tt.Frame()
	if !app.dark {
		t.Fatal("a dark system did not switch the palette")
	}
}
