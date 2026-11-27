//go:build darwin && !ios

package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"golang.org/x/sys/unix"
)

var loadAppKit = sync.OnceValue(func() error {
	_, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	return err
})

func (m *Manager) ReopenWindows(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pids, err := quitInstalledWindows(m.target, m.Status().Backup)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		// The service can switch versions on its own when the desktop is closed.
		return nil
	}
	if err := waitForWindowsToExit(ctx, pids); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/open", "-g", "-n", m.target).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("Open updated Miya: %w (%s)", err, detail)
		}
		return fmt.Errorf("Open updated Miya: %w", err)
	}
	return nil
}

func quitInstalledWindows(target, backup string) ([]int, error) {
	if err := loadAppKit(); err != nil {
		return nil, fmt.Errorf("Find running Miya windows: %w", err)
	}
	// A normal AppKit quit addresses the running instance directly. After the
	// bundle swap its reported URL can refer to either the target or the backup.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	sel := objc.RegisterName
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	defer pool.Send(sel("drain"))
	id := objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), "com.daodao.miya")
	apps := objc.ID(objc.GetClass("NSRunningApplication")).Send(sel("runningApplicationsWithBundleIdentifier:"), id)
	const regularApplication = 0
	var pids []int
	for i := range objc.Send[uint](apps, sel("count")) {
		app := apps.Send(sel("objectAtIndex:"), i)
		pid := int(objc.Send[int32](app, sel("processIdentifier")))
		// The ordinary GUI uses the regular policy. The service and helpers can
		// share its bundle identity, but must never receive the quit request.
		if pid <= 0 || pid == os.Getpid() || objc.Send[int](app, sel("activationPolicy")) != regularApplication {
			continue
		}
		path := objc.Send[string](app.Send(sel("bundleURL")).Send(sel("path")), sel("UTF8String"))
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if path != target && path != backup {
			continue
		}
		if !objc.Send[bool](app, sel("terminate")) && unix.Kill(pid, 0) != unix.ESRCH {
			return nil, fmt.Errorf("Miya window process %d refused the quit request", pid)
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func waitForWindowsToExit(ctx context.Context, pids []int) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for _, pid := range pids {
		// AppKit termination is asynchronous. The service has no AppKit main
		// run loop to refresh isTerminated, so query the process directly.
		for unix.Kill(pid, 0) != unix.ESRCH {
			select {
			case <-ctx.Done():
				return fmt.Errorf("Miya window process %d did not exit: %w", pid, ctx.Err())
			case <-ticker.C:
			}
		}
	}
	return ctx.Err()
}
