package main

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
func createSignal[T any](initial T) (func() T, func(T)) {
	value := initial
	return func() T { return value }, func(next T) {
		value = next
		invalidate()
	}
}

var (
	windowMu  sync.Mutex
	appWindow *mygo.Window
	// pending holds work dispatched before the window exists, as the first
	// connection attempt may finish before WhenReady opens it.
	pending []func()
)

func setAppWindow(w *mygo.Window) {
	windowMu.Lock()
	appWindow = w
	queued := pending
	pending = nil
	windowMu.Unlock()
	if w == nil {
		return
	}
	for _, f := range queued {
		w.Update(f)
	}
}

func currentWindow() *mygo.Window {
	windowMu.Lock()
	defer windowMu.Unlock()
	return appWindow
}

// runOnMain runs f on the UI thread and draws a new frame afterwards. Without
// a window (tests, or before the window opens), work runs inline or waits for
// the window.
var runOnMain = func(f func()) {
	windowMu.Lock()
	w := appWindow
	if w == nil && !inlineDispatch.Load() {
		pending = append(pending, f)
		windowMu.Unlock()
		return
	}
	windowMu.Unlock()
	if w == nil {
		f()
		return
	}
	w.Update(f)
}

// inlineDispatch makes dispatched work run immediately, for tests that drive
// the controller without a window.
var inlineDispatch atomic.Bool

// invalidateHook hears every change of state, as the service publishes it;
// it is read from any goroutine.
var invalidateHook atomic.Pointer[func()]

func setInvalidateHook(f func()) {
	if f == nil {
		invalidateHook.Store(nil)
		return
	}
	invalidateHook.Store(&f)
}

func currentInvalidateHook() func() {
	if p := invalidateHook.Load(); p != nil {
		return *p
	}
	return nil
}

func invalidate() {
	if w := currentWindow(); w != nil {
		w.Invalidate()
	}
	if h := currentInvalidateHook(); h != nil {
		h()
	}
}

// cleanups run once as the app quits, in place of component cleanups.
var (
	cleanupMu sync.Mutex
	cleanups  []func()
)

func onCleanup(f func()) {
	cleanupMu.Lock()
	cleanups = append(cleanups, f)
	cleanupMu.Unlock()
}

func runCleanups() {
	cleanupMu.Lock()
	fns := cleanups
	cleanups = nil
	cleanupMu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}
