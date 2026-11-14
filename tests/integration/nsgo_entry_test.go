package integration

import (
	"go/build"
	"os"
	"testing"
)

// Platform startup selects exactly one NSGo entry without custom build tags.
func TestNSGoPlatformEntrypoints(t *testing.T) {
	for _, file := range []string{"main.go", "main_android.go"} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("retired entry %s: %v", file, err)
		}
	}
	for _, platform := range []string{"darwin", "linux", "windows", "ios", "android"} {
		for _, cgo := range []bool{false, true} {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled, ctx.BuildTags = platform, "arm64", cgo, nil
			mobile := platform == "ios" || platform == "android"
			for file, want := range map[string]bool{"main_nsgo.go": !mobile, "main_nsgo_mobile.go": mobile} {
				got, err := ctx.MatchFile(".", file)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s cgo=%v: %s selected=%v, want %v", platform, cgo, file, got, want)
				}
			}
		}
	}
}
