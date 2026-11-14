// Package nsui is Miya's desktop interface on NSGo. It replaces internal/ui,
// which is written against MyGo, view by view; see docs/nsgo-migration.md.
package nsui

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/uiruntime"
)

// The controller was written for MyGo's immediate mode: its state is
// getters, and a setter calls uiruntime.Invalidate to ask for a new frame.
// NSGo's components run once and update through signals, so the bridge
// turns every Invalidate into one bump of a revision signal. A binding made
// with Live reads the revision before the getter, so it runs again after
// each change and NSGo sends only what actually differs. The controller
// keeps its getters; views that need finer updates can wrap a getter in a
// memo.

// bridge is the app's window as the controller sees it.
type bridge struct {
	cx  *core.Scope
	rev *core.Signal[uint64]

	mu  sync.Mutex
	win *core.Window

	// pending coalesces Invalidate calls made between two engine wake-ups.
	pending atomic.Bool
	// focused and minimized mirror the window's state for the controller,
	// which asks from any goroutine; NSGo's signals are the engine's.
	focused, minimized atomic.Bool
}

// current is the running bridge. Live reads it; it is set once, as the app
// starts, before any view builds.
var current *bridge

// installBridge makes the app's scope the target of the controller's
// dispatch and invalidation.
func installBridge(cx *core.Scope) *bridge {
	b := &bridge{cx: cx, rev: core.NewSignal(cx, uint64(0))}
	current = b
	uiruntime.SetAppWindow(b)
	cx.OnQuit(func(context.Context) {
		uiruntime.SetAppWindow(nil)
		uiruntime.RunCleanups()
	})
	return b
}

// setWindow records the main window, which Show, Focus and the queries act on.
func (b *bridge) setWindow(w *core.Window) {
	b.mu.Lock()
	b.win = w
	b.mu.Unlock()
	if w == nil {
		return
	}
	b.cx.Effect(func() {
		shown := !w.Closed() && w.Visible()
		focused := shown && w.IsFocused()
		was := b.focused.Swap(focused)
		b.minimized.Store(!shown || w.IsMinimized())
		// Coming to the front, the window shows what it holds as of now, as a
		// conversation the user has now seen: bindings read again.
		if focused && !was {
			b.Invalidate()
		}
	})
}

func (b *bridge) window() *core.Window {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.win
}

// bump advances the revision. It runs on the engine goroutine.
func (b *bridge) bump() {
	b.pending.Store(false)
	b.rev.Update(func(n uint64) uint64 { return n + 1 })
}

// Update runs f on the engine goroutine, then lets bindings see its changes.
func (b *bridge) Update(f func()) {
	b.cx.Post(func() {
		f()
		b.bump()
	})
}

// Invalidate may be called from any goroutine, many times a frame.
func (b *bridge) Invalidate() {
	if !b.pending.CompareAndSwap(false, true) {
		return
	}
	b.cx.Post(b.bump)
}

func (b *bridge) Show() {
	b.cx.Post(func() {
		if w := b.window(); w != nil {
			w.Show()
		}
	})
}

func (b *bridge) Focus() {
	b.cx.Post(func() {
		if w := b.window(); w != nil {
			w.Show()
			w.Focus()
		}
	})
}

// Restore shows a hidden or minimized window; NSGo's Show does both.
func (b *bridge) Restore() { b.Show() }

// IsFocused reports whether the main window is the key window.
func (b *bridge) IsFocused() bool { return b.focused.Load() }

// IsMinimized reports whether the main window is minimized, hidden or gone.
func (b *bridge) IsMinimized() bool { return b.minimized.Load() }

// Live returns get as a binding that runs again whenever the controller
// invalidates. Use it for XFunc fields and region conditions that read
// controller state.
func Live[T any](get func() T) func() T {
	return func() T {
		if b := current; b != nil {
			b.rev.Get()
		}
		return get()
	}
}
