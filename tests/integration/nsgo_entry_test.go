package integration

import (
	"go/build"
	"testing"
)

// Both runtimes own platform startup. Never compile the legacy Android JNI
// initialization into an NSGo phone, even in a cgo-enabled build.
func TestNSGoPlatformEntrypoints(t *testing.T) {
	files := []string{"main.go", "main_nsgo.go", "main_nsgo_mobile.go", "main_android.go"}
	for _, tc := range []struct {
		name, os string
		nsgo     bool
		want     map[string]bool
	}{
		{"default-desktop", "darwin", false, map[string]bool{"main.go": true}},
		{"nsgo-desktop", "darwin", true, map[string]bool{"main_nsgo.go": true}},
		{"default-ios", "ios", false, map[string]bool{"main.go": true}},
		{"nsgo-ios", "ios", true, map[string]bool{"main_nsgo_mobile.go": true}},
		{"default-android", "android", false, map[string]bool{"main.go": true, "main_android.go": true}},
		{"nsgo-android", "android", true, map[string]bool{"main_nsgo_mobile.go": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS = tc.os
			ctx.GOARCH = "arm64"
			ctx.CgoEnabled = true
			ctx.BuildTags = nil
			if tc.nsgo {
				ctx.BuildTags = []string{"nsgo"}
			}
			for _, file := range files {
				got, err := ctx.MatchFile(".", file)
				if err != nil {
					t.Fatal(err)
				}
				if got != tc.want[file] {
					t.Errorf("%s selected=%v, want %v", file, got, tc.want[file])
				}
			}
		})
	}
}
