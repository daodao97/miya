package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control/controltest"
	"miya/internal/mobile"
)

func TestDesktopApplicationPickerLaunchesAndShowsWindow(t *testing.T) {
	_, wait := unlockUI(t)
	d, token, calls := controltest.Application(t)
	d.Windows = func() ([]desktop.Window, error) {
		return []desktop.Window{{ID: 5, PID: 9, App: "Notes", BundleID: "com.apple.Notes", Title: "Draft"}}, nil
	}
	jpeg := windowJPEG(t)
	d.CaptureWindow = func(desktop.Window) ([]byte, error) { return jpeg, nil }
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Client, p.Pickable, p.AppsAvailable, p.Phase, p.Sending = controltest.Client(t, d, token), true, true, "live", false
	m.DesktopChooseApps(p)
	wait(func() bool { return p.Phase == "picker" && !p.Busy })
	if len(p.Apps) != 1 || p.Active || p.Image != nil {
		t.Fatal("app list did not replace the live screen")
	}
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
	tt.Frame()
	tt.Frame()
	release := d.HoldWork()
	time.AfterFunc(300*time.Millisecond, release)
	if err := tt.Click("Open Notes"); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return p.Phase == "live" && p.Active })
	if calls.Load() != 1 || !p.WindowOnly || p.Window.ID != 5 {
		t.Fatal("app did not open its window", calls.Load(), p.Window)
	}
	// Complete the in-flight frame before disposing the test UI dispatcher.
	wait(func() bool { return p.Image != nil })
	p.Cancel()
	if _, err := p.Client.Session(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopLaunchedWindowMatchesBundleAndIdentity(t *testing.T) {
	app := desktop.Application{BundleID: "com.apple.Notes"}
	old := desktop.Window{ID: 1, PID: 2, BundleID: app.BundleID}
	fresh := desktop.Window{ID: 2, PID: 2, BundleID: app.BundleID}
	unrelated := desktop.Window{ID: 3, PID: 4, BundleID: "other", App: "Notes"}
	for _, tc := range []struct {
		name          string
		before, after []desktop.Window
		want          uint32
	}{
		{"new window", []desktop.Window{old}, []desktop.Window{old, fresh, unrelated}, 2},
		{"existing window", []desktop.Window{old}, []desktop.Window{old, unrelated}, 1},
		{"ambiguous", nil, []desktop.Window{old, fresh}, 0},
		{"same name", nil, []desktop.Window{unrelated}, 0},
		{"reused id different pid", []desktop.Window{old, {ID: 2, PID: 9}}, []desktop.Window{old, fresh}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, ok := mobile.LaunchedDesktopWindow(app, tc.before, tc.after)
			if (tc.want != 0) != ok || w.ID != tc.want {
				t.Fatal(w, ok)
			}
		})
	}
	if _, ok := mobile.LaunchedDesktopWindow(desktop.Application{}, nil, []desktop.Window{old}); ok {
		t.Fatal("guessed app identity")
	}
}

func TestDesktopApplicationPickerSearchAndLayout(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			p := liveTestDesktopPage(t, m)
			p.Active, p.Image, p.Pickable, p.AppsAvailable, p.PickerTab = false, nil, true, true, 1
			p.Phase = "picker"
			m.Router().Push("/desktop")
			p.Apps = []desktop.Application{{ID: "/Applications/Notes.app", Name: "Notes"}, {ID: "/Applications/Terminal.app", Name: "Terminal"}, {ID: finderApplicationForTest, Name: "Finder"}, {ID: "/Applications/Long.app", Name: "A long application name"}}
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			tt.Frame()
			for _, s := range []string{"Windows", "Apps", "Search apps", "Notes", "Terminal", "Finder"} {
				if !tt.HasText(s) {
					t.Fatal("missing", s)
				}
			}
			a, _ := tt.Find("Open Notes")
			b, _ := tt.Find("Open Finder")
			if a.Y != b.Y || b.X <= a.X || b.X+b.W > float32(width) {
				t.Fatal("app grid overflows", a, b)
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("desktop-apps-%d", int(width)))
			p.AppSearch = "terminal"
			tt.Frame()
			if !tt.HasText("Terminal") || tt.HasText("Notes") {
				t.Fatal("search did not filter apps")
			}
			p.AppSearch = "no match"
			tt.Frame()
			if !tt.HasText("No apps found.") {
				t.Fatal("missing empty state")
			}
		})
	}
}

const finderApplicationForTest = "/System/Library/CoreServices/Finder.app"

func TestDesktopApplicationEntryOnlyOnSupportedMenuLease(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase, p.Pickable, p.AppsAvailable = "live", true, true
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Open app") {
		t.Fatal("missing app entry")
	}
	p.Pickable = false
	tt.Frame()
	if tt.HasText("Open app") {
		t.Fatal("authorization view offered app launching")
	}
	p.Pickable, p.AppsAvailable = true, false
	tt.Frame()
	if tt.HasText("Open app") {
		t.Fatal("old host offered app launching")
	}
}
