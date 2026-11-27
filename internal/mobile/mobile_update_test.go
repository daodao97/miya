package mobile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDrawerShowsUpdateNextToMiyaOnlyWhenAvailable(t *testing.T) {
	m := NewMobileApp()
	m.ApplyPalette(false)
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { m.drawerContent(c) })
	}, 320, 720)
	label := fmt.Sprintf(tr("Available version: %s"), "0.2.0")
	if _, found := tester.Find(label); found {
		t.Fatal("drawer showed an update before one was available")
	}

	m.appUpdate = mobileUpdate{version: "0.2.0", url: testFlightURL}
	tester.Frame()
	name, foundName := tester.Find("Miya")
	icon, foundIcon := tester.Find(label)
	if !foundName || !foundIcon || icon.X < name.X+name.W || icon.Y > name.Y || icon.Y+icon.H < name.Y+name.H || icon.W < 43.99 || icon.H < 43.99 {
		t.Fatalf("update needs a separate touch target next to Miya: name=%v (%t), icon=%v (%t)", name, foundName, icon, foundIcon)
	}
	if _, found := tester.Find(tr("Switch desktop")); !found {
		t.Fatal("drawer lost its desktop switcher")
	}
}

func TestAndroidUpdateRequiresPublishedAPK(t *testing.T) {
	const asset = `{"name":"miya-0.2.0.apk","state":"uploaded"}`
	for _, tc := range []struct {
		name, version, assets   string
		draft, prerelease, show bool
	}{
		{name: "available", version: "0.1.1", assets: asset, show: true},
		{name: "already installed", version: "0.2.0", assets: asset},
		{name: "missing APK", version: "0.1.1"},
		{name: "incomplete upload", version: "0.1.1", assets: `{"name":"miya-0.2.0.apk","state":"new"}`},
		{name: "draft", version: "0.1.1", assets: asset, draft: true},
		{name: "prerelease", version: "0.1.1", assets: asset, prerelease: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"tag_name":"v0.2.0","draft":%t,"prerelease":%t,"assets":[%s]}`, tc.draft, tc.prerelease, tc.assets)
			got, err := readMobileUpdate(strings.NewReader(body), "android", tc.version, "")
			var want mobileUpdate
			if tc.show {
				want = mobileUpdate{version: "0.2.0", url: "https://github.com/daodao97/miya/releases/download/v0.2.0/miya-0.2.0.apk"}
			}
			if err != nil || got != want {
				t.Fatalf("readMobileUpdate = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestIOSUpdateUsesBuildNumberAndExpiration(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, version, build string
		expires              time.Time
		show                 bool
	}{
		{name: "newer version", version: "0.1.0", build: "30", expires: now.Add(time.Hour), show: true},
		{name: "newer build", version: "0.2.0", build: "25", expires: now.Add(time.Hour), show: true},
		{name: "same build", version: "0.2.0", build: "26", expires: now.Add(time.Hour)},
		{name: "older version", version: "0.3.0", build: "25", expires: now.Add(time.Hour)},
		{name: "expired", version: "0.2.0", build: "25", expires: now.Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"version":"0.2.0","build":"26","expiresAt":%q}`, tc.expires.Format(time.RFC3339))
			got, err := readMobileUpdate(strings.NewReader(body), "ios", tc.version, tc.build)
			var want mobileUpdate
			if tc.show {
				want = mobileUpdate{version: "0.2.0", url: testFlightURL}
			}
			if err != nil || got != want {
				t.Fatalf("readMobileUpdate = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}
