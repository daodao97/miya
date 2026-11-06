package uiruntime

import (
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
)

// The controller was written against getter/setter pairs. In MyGo's
// immediate-mode UI the view reads them on every frame, so a signal only
// needs to hold its value and ask the window for a new frame when it
// changes. Every setter runs on the main thread (through dispatch), as do
// the views that read it.
func CreateSignal[T any](initial T) (func() T, func(T)) {
	value := initial
	return func() T { return value }, func(next T) {
		value = next
		Invalidate()
	}
}

var WindowMu sync.Mutex

var appWindow *mygo.Window

var Pending []func()

func SetAppWindow(w *mygo.Window) {
	WindowMu.Lock()
	appWindow = w
	queued := Pending
	Pending = nil
	WindowMu.Unlock()
	if w == nil {
		return
	}
	for _, f := range queued {
		w.Update(f)
	}
}

func CurrentWindow() *mygo.Window {
	WindowMu.Lock()
	defer WindowMu.Unlock()
	return appWindow
}

// runOnMain runs f on the UI thread and draws a new frame afterwards. Without
// a window (tests, or before the window opens), work runs inline or waits for
// the window.
var RunOnMain = func(f func()) {
	WindowMu.Lock()
	w := appWindow
	if w == nil && !InlineDispatch.Load() {
		Pending = append(Pending, f)
		WindowMu.Unlock()
		return
	}
	WindowMu.Unlock()
	if w == nil {
		f()
		return
	}
	w.Update(f)
}

// inlineDispatch makes dispatched work run immediately, for tests that drive
// the controller without a window.
var InlineDispatch atomic.Bool

// invalidateHook hears every change of state, as the service publishes it;
// it is read from any goroutine.
var invalidateHook atomic.Pointer[func()]

func SetInvalidateHook(f func()) {
	if f == nil {
		invalidateHook.Store(nil)
		return
	}
	invalidateHook.Store(&f)
}

func CurrentInvalidateHook() func() {
	if p := invalidateHook.Load(); p != nil {
		return *p
	}
	return nil
}

func Invalidate() {
	if w := CurrentWindow(); w != nil {
		w.Invalidate()
	}
	if h := CurrentInvalidateHook(); h != nil {
		h()
	}
}

// cleanups run once as the app quits, in place of component cleanups.
var cleanupMu sync.Mutex

var cleanups []func()

func OnCleanup(f func()) {
	cleanupMu.Lock()
	cleanups = append(cleanups, f)
	cleanupMu.Unlock()
}

func RunCleanups() {
	cleanupMu.Lock()
	fns := cleanups
	cleanups = nil
	cleanupMu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}
