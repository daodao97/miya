//go:build darwin && !ios && cgo

package desktop

/*
#cgo LDFLAGS: -framework Carbon
#include <stdlib.h>
void *miya_privacy_begin(void);
int miya_privacy_tick(void *state);
void miya_privacy_end(void *state);
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// A separate process owns gamma changes. EOF, a three-second heartbeat loss,
// signals and the local hotkey all return through the same restoring path.
func RunPrivacyHelper() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	state := C.miya_privacy_begin()
	if state == nil {
		return errors.New("Privacy mode is not supported by these displays or its recovery shortcut is unavailable")
	}
	defer C.miya_privacy_end(state)
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		return err
	}
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
	last := time.Now()
	for {
		select {
		case <-signals:
			return nil
		case _, ok := <-input:
			if !ok {
				return nil
			}
			last = time.Now()
		case <-tick.C:
			if time.Since(last) > 3*time.Second {
				return nil
			}
			if C.miya_privacy_tick(state) != 0 {
				return nil
			}
		}
	}
}
