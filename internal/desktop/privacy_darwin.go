//go:build darwin && !ios

package desktop

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	dc "github.com/egoist/mygo/plugins/desktopcontrol"
)

// A separate process owns gamma changes. EOF, a three-second heartbeat loss,
// signals and the local hotkey all return through the same restoring path.
func RunPrivacyHelper() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan struct{}, 1)
	go func() {
		defer close(input)
		var b [1]byte
		for {
			if _, err := os.Stdin.Read(b[:]); err != nil {
				return
			}
			select {
			case input <- struct{}{}:
			default:
			}
		}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		defer cancel()
		last := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				return
			case _, ok := <-input:
				if !ok {
					return
				}
				last = time.Now()
			case <-tick.C:
				if time.Since(last) > 3*time.Second {
					return
				}
			}
		}
	}()
	return dc.RunPrivacy(ctx, func() error {
		_, err := fmt.Fprintln(os.Stdout, "ready")
		return err
	})
}
