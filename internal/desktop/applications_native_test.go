//go:build darwin && !ios && cgo

package desktop

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestApplicationsNative(t *testing.T) {
	if os.Getenv("MIYA_DESKTOP_APPS_PROBE") != "1" {
		t.Skip("opt-in installed application enumeration")
	}
	apps, err := Applications()
	if err != nil {
		t.Fatal(err)
	}
	icons, finder := 0, false
	for _, app := range apps {
		if app.ID == "" || app.Name == "" {
			t.Fatal("missing application identity", app)
		}
		if app.ID == finderApplication {
			finder = true
		}
		if len(app.Icon) > 0 {
			config, err := png.DecodeConfig(bytes.NewReader(app.Icon))
			if err != nil || config.Width != 48 || config.Height != 48 {
				t.Fatal("invalid icon", app.ID, config, err)
			}
			icons++
		}
	}
	if !finder || icons == 0 {
		t.Fatal("Finder or icons missing", finder, icons)
	}
	t.Logf("enumerated %d applications with %d native icons", len(apps), icons)
}
