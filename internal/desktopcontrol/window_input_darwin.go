//go:build darwin && !ios

package desktopcontrol

import (
	"fmt"
	"math"
	"structs"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	axValueCGPointType = 1
	axValueCGSizeType  = 2

	nsApplicationActivateIgnoringOtherApps = 1 << 1

	// geometryTolerance is how far, in points, AX and the window server
	// may disagree on a window's frame.
	geometryTolerance = 2
)

// cgSize is CGSize, which AXValueGetValue writes.
type cgSize struct {
	_    structs.HostLayout
	W, H float64
}

var (
	axOnce sync.Once
	axErr  error

	axUIElementCreateApplication   func(pid int32) uintptr
	axUIElementCreateSystemWide    func() uintptr
	axUIElementCopyAttributeValue  func(elem, attr uintptr, out *uintptr) int32
	axUIElementSetAttributeValue   func(elem, attr, value uintptr) int32
	axUIElementPerformAction       func(elem, action uintptr) int32
	axUIElementSetMessagingTimeout func(elem uintptr, seconds float32) int32
	axUIElementGetPid              func(elem uintptr, pid *int32) int32
	axValueGetValue                func(value uintptr, typ uint32, out unsafe.Pointer) bool
	cfRetain                       func(obj uintptr) uintptr
)

func loadAX() error {
	if err := loadInput(); err != nil {
		return err
	}
	if err := loadSession(); err != nil {
		return err
	}
	axOnce.Do(func() {
		as, err := purego.Dlopen(applicationServicesPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			axErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			axErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		for _, f := range []struct {
			fn   any
			lib  uintptr
			name string
		}{
			{&axUIElementCreateApplication, as, "AXUIElementCreateApplication"},
			{&axUIElementCreateSystemWide, as, "AXUIElementCreateSystemWide"},
			{&axUIElementCopyAttributeValue, as, "AXUIElementCopyAttributeValue"},
			{&axUIElementSetAttributeValue, as, "AXUIElementSetAttributeValue"},
			{&axUIElementPerformAction, as, "AXUIElementPerformAction"},
			{&axUIElementSetMessagingTimeout, as, "AXUIElementSetMessagingTimeout"},
			{&axUIElementGetPid, as, "AXUIElementGetPid"},
			{&axValueGetValue, as, "AXValueGetValue"},
			{&cfRetain, cf, "CFRetain"},
		} {
			p, err := purego.Dlsym(f.lib, f.name)
			if err != nil {
				axErr = fmt.Errorf("desktopcontrol: missing %s: %w", f.name, ErrUnsupported)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
	})
	return axErr
}

// axCopy copies an attribute of elem: owned, or 0. Call it in a pool. The
// attribute names are the values of the AX headers' CFSTR constants.
func axCopy(elem uintptr, attr string) uintptr {
	var v uintptr
	if axUIElementCopyAttributeValue(elem, uintptr(nsString(attr)), &v) != 0 {
		return 0
	}
	return v
}

// axFrame reads elem's position and size, in global points.
func axFrame(elem uintptr) (Rect, bool) {
	pos, size := axCopy(elem, "AXPosition"), axCopy(elem, "AXSize")
	if pos != 0 {
		defer cfRelease(pos)
	}
	if size != 0 {
		defer cfRelease(size)
	}
	var p cgPoint
	var s cgSize
	if pos == 0 || size == 0 || !axValueGetValue(pos, axValueCGPointType, unsafe.Pointer(&p)) || !axValueGetValue(size, axValueCGSizeType, unsafe.Pointer(&s)) {
		return Rect{}, false
	}
	return Rect{p.X, p.Y, s.W, s.H}, true
}

func sameFrame(a, b Rect) bool {
	return math.Abs(a.X-b.X) <= geometryTolerance && math.Abs(a.Y-b.Y) <= geometryTolerance &&
		math.Abs(a.Width-b.Width) <= geometryTolerance && math.Abs(a.Height-b.Height) <= geometryTolerance
}

// copyWindowElement finds the AX window of pid whose frame is bounds:
// owned, or 0. AX has no public window ID, so an ambiguous match is
// refused rather than raising another window of the app.
func copyWindowElement(bounds Rect, pid int) uintptr {
	app := axUIElementCreateApplication(int32(pid))
	if app == 0 {
		return 0
	}
	defer cfRelease(app)
	axUIElementSetMessagingTimeout(app, 0.2)
	list := axCopy(app, "AXWindows")
	if list == 0 {
		return 0
	}
	defer cfRelease(list)
	arr := objc.ID(list)
	var match uintptr
	for i := range objc.Send[uint](arr, sel("count")) {
		window := uintptr(arr.Send(sel("objectAtIndex:"), i))
		frame, ok := axFrame(window)
		if !ok || !sameFrame(frame, bounds) {
			continue
		}
		if match != 0 {
			cfRelease(match)
			return 0
		}
		match = cfRetain(window)
	}
	return match
}

// focusWindow brings the window that bounds names to the front of pid,
// and pid to the front.
func focusWindow(bounds Rect, pid int) bool {
	// Some apps (including WeChat) do not expose AX windows. Activation
	// can still bring their existing front window forward. The caller must
	// verify the exact window ID/PID at the input point before posting.
	activated := false
	if app := class("NSRunningApplication").Send(sel("runningApplicationWithProcessIdentifier:"), int32(pid)); app != 0 {
		activated = objc.Send[bool](app, sel("activateWithOptions:"), uint(nsApplicationActivateIgnoringOtherApps))
	}
	match := copyWindowElement(bounds, pid)
	if match == 0 {
		return activated
	}
	defer cfRelease(match)
	axUIElementPerformAction(match, uintptr(nsString("AXRaise")))
	if app := axUIElementCreateApplication(int32(pid)); app != 0 {
		axUIElementSetAttributeValue(app, uintptr(nsString("AXFocusedWindow")), match)
		cfRelease(app)
	}
	return true
}

// frontPID is the focused app's process. A background process has no
// AppKit run loop to keep NSWorkspace's frontmostApplication current, so
// it asks accessibility.
func frontPID() int {
	system := axUIElementCreateSystemWide()
	if system == 0 {
		return 0
	}
	defer cfRelease(system)
	focused := axCopy(system, "AXFocusedApplication")
	if focused == 0 {
		return 0
	}
	defer cfRelease(focused)
	var pid int32
	if axUIElementGetPid(focused, &pid) != 0 {
		return 0
	}
	return int(pid)
}

func contains(r Rect, p cgPoint) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

// windowIsFront reports whether w's app is in front and w is the topmost
// window under at (pointer) or the frontmost window of its app.
func windowIsFront(w Window, at cgPoint, pointer bool) bool {
	focused := frontPID()
	if focused != w.PID {
		// AXFocusedApplication can fail for an otherwise active app (for
		// example WeChat). Query that process's current activation state,
		// not NSWorkspace's cached frontmostApplication. Window-server
		// identity and occlusion are still checked below.
		if focused != 0 {
			return false
		}
		app := class("NSRunningApplication").Send(sel("runningApplicationWithProcessIdentifier:"), int32(w.PID))
		if app == 0 || !objc.Send[bool](app, sel("isActive")) {
			return false
		}
	}
	for _, r := range onScreen() {
		if r.alpha <= 0 || !r.ok {
			continue
		}
		if pointer && !contains(r.bounds, at) || !pointer && r.pid != w.PID {
			continue
		}
		return r.id == w.ID && r.pid == w.PID
	}
	return false
}

// currentBounds is w's frame now, if it is still on screen.
func currentBounds(w Window) (Rect, bool) {
	for _, r := range onScreen() {
		if r.id == w.ID && r.pid == w.PID && r.ok {
			return r.bounds, true
		}
	}
	return Rect{}, false
}

func postToWindow(w Window, in Input) error {
	if err := in.validate(); err != nil {
		return err
	}
	if w.ID == 0 || w.PID <= 0 {
		return ErrWindowUnavailable
	}
	if err := loadAX(); err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return ErrAccessibility
	}
	var err error
	withPool(func() {
		bounds, ok := currentBounds(w)
		if !ok {
			err = ErrWindowUnavailable
			return
		}
		at := cgPoint{X: in.X, Y: in.Y}
		pointer := in.Kind != Text && in.Kind != Key
		if pointer && (!contains(bounds, at) || in.Kind == Drag && !contains(bounds, cgPoint{X: in.ToX, Y: in.ToY})) {
			err = errInvalidInput
			return
		}
		if in.Kind == Move && !windowIsFront(w, at, true) {
			return
		}
		if !windowIsFront(w, at, pointer) {
			if !focusWindow(bounds, w.PID) {
				err = ErrWindowUnavailable
				return
			}
			for i := 0; i < 10 && !windowIsFront(w, at, pointer); i++ {
				time.Sleep(20 * time.Millisecond)
			}
			if !windowIsFront(w, at, pointer) {
				err = ErrWindowUnavailable
				return
			}
		}
		if pointer {
			err = postGuarded(in, 0, nil)
			return
		}
		err = postGuarded(in, w.PID, func() bool { return windowIsFront(w, at, false) })
	})
	return err
}
