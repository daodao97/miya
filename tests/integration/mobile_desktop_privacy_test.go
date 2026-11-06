package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop/control"
	"miya/internal/desktop/control/controltest"
)

func TestDesktopPrivacyPageToggleAndServerState(t *testing.T) {
	_, wait := unlockUI(t)
	d, token, _, current := controltest.Privacy(t)
	controltest.ReadyPrivacy(t, d, token)
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Client, p.Pickable, p.Phase = controltest.Client(t, d, token), true, "live"
	m.DesktopInitPrivacy(p, control.Session{Pick: true, Privacy: true})
	wait(func() bool { return p.Privacy.Known })
	if p.Privacy.Enabled {
		t.Fatal("privacy must start off")
	}
	m.DesktopSetPrivacy(p)
	wait(func() bool { return !p.Privacy.Busy })
	if !p.Privacy.Enabled || !p.Privacy.Known || *current == nil {
		t.Fatal("did not enable")
	}
	m.DesktopSetPrivacy(p)
	wait(func() bool { return !p.Privacy.Busy })
	if p.Privacy.Enabled || !p.Privacy.Known {
		t.Fatal("did not restore")
	}
	p.Cancel()
}

func TestDesktopPrivacyPanelLayoutAndCapability(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			p := liveTestDesktopPage(t, m)
			p.Phase, p.Pickable, p.Privacy.Available, p.Privacy.Known, p.More = "live", true, true, true, true
			m.Router().Push("/desktop")
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			tt.Frame()
			for _, text := range []string{"Privacy mode", "Enable privacy mode", "Control–Option–Command–P"} {
				if !tt.HasText(text) {
					t.Fatal("missing", text)
				}
			}
			r, ok := tt.Find("Enable privacy mode")
			if !ok || r.X < 0 || r.Y < 56 || r.X+r.W > float32(width) || r.Y+r.H > 750 {
				t.Fatal("privacy control out of bounds", r)
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("desktop-privacy-%d", width))
			// The sheet remains reachable when the software keyboard leaves a
			// short viewport and the person uses larger text.
			short := ui.NewTester(m.ViewRoot, width, 440)
			short.SetPreferences(ui.Preferences{TextScale: 1.6})
			short.Frame()
			short.Frame()
			panel, exists := short.Find("Privacy mode")
			if !exists || panel.Y < 56 || panel.X < 0 || panel.X+panel.W > float32(width) {
				t.Fatal("privacy panel leaves short viewport", panel)
			}
			saveMobileNativeSnapshot(t, short, fmt.Sprintf("desktop-privacy-short-%d", width))
			p.Privacy.Enabled = true
			tt.Frame()
			if !tt.HasText("Restore Mac displays") {
				t.Fatal("missing restore")
			}
			p.Pickable = false
			tt.Frame()
			if tt.HasText("Privacy mode") {
				t.Fatal("approval view offered privacy")
			}
			p.Pickable, p.Privacy.Available = true, false
			tt.Frame()
			if tt.HasText("Privacy mode") {
				t.Fatal("old host offered privacy")
			}
		})
	}
}

func TestDesktopPrivacyUncertainStateCanStillRestore(t *testing.T) {
	_, wait := unlockUI(t)
	d, token, _, _ := controltest.Privacy(t)
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Client, p.Pickable, p.Privacy.Available, p.Privacy.Known = controltest.Client(t, d, token), true, true, false
	m.DesktopSetPrivacy(p)
	wait(func() bool { return !p.Privacy.Busy })
	if !p.Privacy.Known || p.Privacy.Enabled {
		t.Fatal("uncertain status could not restore")
	}
	// A missing image must never allow the display to be blanked.
	p.Image = nil
	m.DesktopSetPrivacy(p)
	if p.Privacy.Busy {
		t.Fatal("enabled before a frame")
	}
	p.Cancel()
	_, _ = p.Client.Privacy(context.Background(), nil)
}
