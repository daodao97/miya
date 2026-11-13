//go:build darwin && !ios

package desktopcontrol

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	ioKitPath = "/System/Library/Frameworks/IOKit.framework/IOKit"

	cgWindowListOptionOnScreenOnly     = 1 << 0
	cgWindowListExcludeDesktopElements = 1 << 4
	kIOPMUserActiveLocal               = 0
)

var (
	sessionOnce sync.Once
	sessionErr  error

	cgWindowListCopyWindowInfo             func(option, relativeTo uint32) uintptr
	cgRectMakeWithDictionaryRepresentation func(dict uintptr, rect *cgRect) bool
	cgPreflightScreenCaptureAccess         func() bool
	cgSessionCopyCurrentDictionary         func() uintptr
	cgDisplayIsAsleep                      func(display uint32) bool
	iopmAssertionDeclareUserActivity       func(name uintptr, kind int32, id *uint32) int32

	// The keys of CGWindowListCopyWindowInfo's dictionaries.
	kWindowNumber, kWindowOwnerPID, kWindowOwnerName, kWindowName, kWindowLayer, kWindowAlpha, kWindowBounds objc.ID

	macMajor int
)

// constID reads an exported CFString constant such as kCGWindowNumber.
func constID(lib uintptr, name string) (objc.ID, error) {
	p, err := purego.Dlsym(lib, name)
	if err != nil || p == 0 {
		return 0, fmt.Errorf("desktopcontrol: missing %s: %w", name, ErrUnsupported)
	}
	return **(**objc.ID)(unsafe.Pointer(&p)), nil
}

func loadSession() error {
	if err := load(); err != nil {
		return err
	}
	sessionOnce.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			sessionErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		iokit, err := purego.Dlopen(ioKitPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			sessionErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		for _, f := range []struct {
			fn   any
			lib  uintptr
			name string
		}{
			{&cgWindowListCopyWindowInfo, cg, "CGWindowListCopyWindowInfo"},
			{&cgRectMakeWithDictionaryRepresentation, cg, "CGRectMakeWithDictionaryRepresentation"},
			{&cgPreflightScreenCaptureAccess, cg, "CGPreflightScreenCaptureAccess"},
			{&cgSessionCopyCurrentDictionary, cg, "CGSessionCopyCurrentDictionary"},
			{&cgDisplayIsAsleep, cg, "CGDisplayIsAsleep"},
			{&iopmAssertionDeclareUserActivity, iokit, "IOPMAssertionDeclareUserActivity"},
		} {
			p, err := purego.Dlsym(f.lib, f.name)
			if err != nil {
				sessionErr = fmt.Errorf("desktopcontrol: missing %s: %w", f.name, ErrUnsupported)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
		for _, k := range []struct {
			dst  *objc.ID
			name string
		}{
			{&kWindowNumber, "kCGWindowNumber"}, {&kWindowOwnerPID, "kCGWindowOwnerPID"},
			{&kWindowOwnerName, "kCGWindowOwnerName"}, {&kWindowName, "kCGWindowName"},
			{&kWindowLayer, "kCGWindowLayer"}, {&kWindowAlpha, "kCGWindowAlpha"},
			{&kWindowBounds, "kCGWindowBounds"},
		} {
			if *k.dst, sessionErr = constID(cg, k.name); sessionErr != nil {
				return
			}
		}
		withPool(func() {
			type osVersion struct{ Major, Minor, Patch int }
			info := class("NSProcessInfo").Send(sel("processInfo"))
			macMajor = objc.Send[osVersion](info, sel("operatingSystemVersion")).Major
		})
	})
	return sessionErr
}

func available() bool { return loadSession() == nil && macMajor >= 14 }

// windowRecord is one dictionary of CGWindowListCopyWindowInfo.
type windowRecord struct {
	id     uint32
	pid    int
	owner  string
	title  string
	layer  int
	alpha  float64
	bounds Rect
	ok     bool // bounds were readable
}

// onScreen reads the on-screen windows, front to back. Call it in a pool.
func onScreen() []windowRecord {
	list := cgWindowListCopyWindowInfo(cgWindowListOptionOnScreenOnly|cgWindowListExcludeDesktopElements, 0)
	if list == 0 {
		return nil
	}
	defer cfRelease(list)
	arr := objc.ID(list)
	get := func(dict, key objc.ID) objc.ID { return dict.Send(sel("objectForKey:"), key) }
	n := objc.Send[uint](arr, sel("count"))
	out := make([]windowRecord, 0, n)
	for i := range n {
		d := arr.Send(sel("objectAtIndex:"), i)
		r := windowRecord{owner: goString(get(d, kWindowOwnerName)), title: goString(get(d, kWindowName)), alpha: 1}
		if v := get(d, kWindowNumber); v != 0 {
			r.id = objc.Send[uint32](v, sel("unsignedIntValue"))
		}
		if v := get(d, kWindowOwnerPID); v != 0 {
			r.pid = int(objc.Send[int32](v, sel("intValue")))
		}
		if v := get(d, kWindowLayer); v != 0 {
			r.layer = int(objc.Send[int32](v, sel("intValue")))
		}
		if v := get(d, kWindowAlpha); v != 0 {
			r.alpha = objc.Send[float64](v, sel("doubleValue"))
		}
		var b cgRect
		if v := get(d, kWindowBounds); v != 0 && cgRectMakeWithDictionaryRepresentation(uintptr(v), &b) {
			r.bounds, r.ok = Rect{b.X, b.Y, b.W, b.H}, true
		}
		out = append(out, r)
	}
	return out
}

func windows() ([]Window, error) {
	if !available() {
		return nil, ErrUnsupported
	}
	if !cgPreflightScreenCaptureAccess() {
		return nil, ErrScreenRecording
	}
	var out []Window
	withPool(func() {
		for _, r := range onScreen() {
			if r.id == 0 || r.pid <= 0 || r.layer < 0 || r.alpha <= 0 || !r.ok {
				continue
			}
			w := Window{ID: r.id, PID: r.pid, App: r.owner, Title: r.title, Bounds: r.bounds, Layer: r.layer}
			if app := class("NSRunningApplication").Send(sel("runningApplicationWithProcessIdentifier:"), int32(r.pid)); app != 0 {
				w.BundleID = goString(app.Send(sel("bundleIdentifier")))
				if name := goString(app.Send(sel("localizedName"))); name != "" {
					w.App = name
				}
			}
			out = append(out, w)
		}
	})
	return out, nil
}

func locked() (bool, error) {
	if err := loadSession(); err != nil {
		return false, err
	}
	var v, ok bool
	withPool(func() {
		dict := cgSessionCopyCurrentDictionary()
		if dict == 0 {
			return
		}
		defer cfRelease(dict)
		ok = true
		if flag := objc.ID(dict).Send(sel("objectForKey:"), nsString("CGSSessionScreenIsLocked")); flag != 0 {
			v = objc.Send[bool](flag, sel("boolValue"))
		}
	})
	if !ok {
		return false, errors.New("desktopcontrol: the console session is unavailable")
	}
	return v, nil
}

var (
	wakeMu sync.Mutex
	// wakeActivity is the one assertion that every Wake renews.
	wakeActivity uint32
)

func wake() error {
	if err := loadSession(); err != nil {
		return err
	}
	wakeMu.Lock()
	defer wakeMu.Unlock()
	display := cgMainDisplayID()
	asleep := cgDisplayIsAsleep(display)
	var status int32
	withPool(func() {
		status = iopmAssertionDeclareUserActivity(uintptr(nsString("Remote control")), kIOPMUserActiveLocal, &wakeActivity)
	})
	if status != 0 {
		return fmt.Errorf("desktopcontrol: declaring user activity failed (%#x)", uint32(status))
	}
	for i := 0; asleep && i < 40 && cgDisplayIsAsleep(display); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}
