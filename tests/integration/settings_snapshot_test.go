package integration

import (
	"fmt"
	"testing"

	"miya/internal/widgets"
)

// MIYA_SNAPSHOT=dir go test ./tests/integration -run TestSettingsSnapshot
// writes every settings page, light and dark, in a tall window to look at.
func TestSettingsSnapshot(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		app, tt := testApp(t)
		t.Cleanup(func() { widgets.ApplyDesktopPalette(false) })
		app.Appearance = appearance
		app.Section = "Settings"
		tt.SetSize(1180, 1700)
		for tab, name := range []string{"general", "account", "phone", "memory"} {
			app.SettingsTab = tab
			tt.Frame()
			tt.Frame()
			saveReconnectSnapshot(t, tt, fmt.Sprintf("settings-%s-%s", name, appearance))
		}
	}
}
