//go:build !mygo_ui

package integration

import (
	"miya/internal/desktop"
	mobile "miya/internal/phone/client"
	"testing"
)

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

const finderApplicationForTest = "/System/Library/CoreServices/Finder.app"
