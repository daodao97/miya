//go:build darwin && !ios

package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReopenWindowsWithoutRunningTarget(t *testing.T) {
	m := &Manager{target: filepath.Join(t.TempDir(), "Miya.app")}
	// No window belongs to this installation. Reopening must skip the
	// nonexistent app, allowing even a headless service to switch versions.
	if err := m.ReopenWindows(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForWindowsToExitTimesOutIfWindowRefusesToQuit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := waitForWindowsToExit(ctx, []int{os.Getpid()}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting on a live process returned %v", err)
	}
}
