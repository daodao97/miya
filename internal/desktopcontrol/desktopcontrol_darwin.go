//go:build darwin && !ios

package desktopcontrol

import (
	"fmt"
	"runtime"
	"sort"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	coreGraphicsPath   = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	appKitPath         = "/System/Library/Frameworks/AppKit.framework/AppKit"
	colorSyncPath      = "/System/Library/Frameworks/ColorSync.framework/ColorSync"
	libObjCPath        = "/usr/lib/libobjc.A.dylib"

	nsApplicationActivationPolicyProhibited = 2
)

// cgRect is CGRect, which CGDisplayBounds returns by value.
type cgRect struct {
	_          structs.HostLayout
	X, Y, W, H float64
}

var (
	loadOnce sync.Once
	loadErr  error

	cgGetActiveDisplayList           func(max uint32, ids *uint32, count *uint32) int32
	cgDisplayCreateUUIDFromDisplayID func(display uint32) uintptr
	cgDisplayBounds                  func(display uint32) cgRect
	cgDisplayPixelsWide              func(display uint32) uintptr
	cgDisplayPixelsHigh              func(display uint32) uintptr
	cgMainDisplayID                  func() uint32
	cgDisplayIsActive                func(display uint32) bool
	cfUUIDCreateString               func(alloc, uuid uintptr) uintptr
	cfRelease                        func(obj uintptr)

	poolPush, poolPop uintptr
	// nsApp is the address of AppKit's NSApp global.
	nsApp uintptr
)

func sel(name string) objc.SEL { return objc.RegisterName(name) }

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func load() error {
	loadOnce.Do(func() {
		libs := map[string]uintptr{}
		for _, path := range []string{coreGraphicsPath, coreFoundationPath, appKitPath, colorSyncPath, libObjCPath} {
			h, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			if err != nil {
				loadErr = fmt.Errorf("desktopcontrol: %w", err)
				return
			}
			libs[path] = h
		}
		for _, f := range []struct {
			fn        any
			lib, name string
		}{
			{&cgGetActiveDisplayList, coreGraphicsPath, "CGGetActiveDisplayList"},
			{&cgDisplayCreateUUIDFromDisplayID, colorSyncPath, "CGDisplayCreateUUIDFromDisplayID"},
			{&cgDisplayBounds, coreGraphicsPath, "CGDisplayBounds"},
			{&cgDisplayPixelsWide, coreGraphicsPath, "CGDisplayPixelsWide"},
			{&cgDisplayPixelsHigh, coreGraphicsPath, "CGDisplayPixelsHigh"},
			{&cgMainDisplayID, coreGraphicsPath, "CGMainDisplayID"},
			{&cgDisplayIsActive, coreGraphicsPath, "CGDisplayIsActive"},
			{&cfUUIDCreateString, coreFoundationPath, "CFUUIDCreateString"},
			{&cfRelease, coreFoundationPath, "CFRelease"},
		} {
			p, err := purego.Dlsym(libs[f.lib], f.name)
			if err != nil {
				loadErr = fmt.Errorf("desktopcontrol: missing %s: %w", f.name, ErrUnsupported)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
		var err error
		if poolPush, err = purego.Dlsym(libs[libObjCPath], "objc_autoreleasePoolPush"); err == nil {
			poolPop, err = purego.Dlsym(libs[libObjCPath], "objc_autoreleasePoolPop")
		}
		if err == nil {
			nsApp, err = purego.Dlsym(libs[appKitPath], "NSApp")
		}
		if err != nil {
			loadErr = fmt.Errorf("desktopcontrol: %w", err)
		}
	})
	return loadErr
}

// withPool runs fn in an autorelease pool, on one thread.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p, _, _ := purego.SyscallN(poolPush)
	defer purego.SyscallN(poolPop, p)
	fn()
}

func cstr(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

// nsString returns an autoreleased NSString.
func nsString(s string) objc.ID {
	return class("NSString").Send(sel("stringWithUTF8String:"), cstr(s))
}

func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	p := objc.Send[unsafe.Pointer](s, sel("UTF8String"))
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// displayUUID is the screen's own identity, empty when it has none.
func displayUUID(display uint32) string {
	u := cgDisplayCreateUUIDFromDisplayID(display)
	if u == 0 {
		return ""
	}
	defer cfRelease(u)
	s := cfUUIDCreateString(0, u)
	if s == 0 {
		return ""
	}
	defer cfRelease(s)
	return goString(objc.ID(s))
}

// displayMatches reports whether display is active and still the screen
// that uuid names.
func displayMatches(display uint32, uuid string) bool {
	return display != 0 && uuid != "" && cgDisplayIsActive(display) && displayUUID(display) == uuid
}

// screenNames maps display IDs to NSScreen's localized names. NSScreen
// needs an application object: an app has one; a background process gets
// one that may never activate, own a Dock icon or take focus. An existing
// application's policy is left alone.
func screenNames() map[uint32]string {
	if **(**objc.ID)(unsafe.Pointer(&nsApp)) == 0 {
		app := class("NSApplication").Send(sel("sharedApplication"))
		app.Send(sel("setActivationPolicy:"), nsApplicationActivationPolicyProhibited)
	}
	names := map[uint32]string{}
	screens := class("NSScreen").Send(sel("screens"))
	key := nsString("NSScreenNumber")
	for i := range objc.Send[uint](screens, sel("count")) {
		screen := screens.Send(sel("objectAtIndex:"), i)
		number := screen.Send(sel("deviceDescription")).Send(sel("objectForKey:"), key)
		if number == 0 {
			continue
		}
		if name := goString(screen.Send(sel("localizedName"))); name != "" {
			names[objc.Send[uint32](number, sel("unsignedIntValue"))] = name
		}
	}
	return names
}

func displays() ([]Display, error) {
	if err := load(); err != nil {
		return nil, err
	}
	var out []Display
	var err error
	withPool(func() {
		var ids [32]uint32
		var n uint32
		if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &n) != 0 || n >= uint32(len(ids)) {
			err = ErrDisplayUnavailable
			return
		}
		names := screenNames()
		main := cgMainDisplayID()
		for _, id := range ids[:n] {
			uuid := displayUUID(id)
			if uuid == "" {
				continue
			}
			b := cgDisplayBounds(id)
			out = append(out, Display{
				ID: id, UUID: uuid, Name: names[id], Main: id == main,
				Bounds:     Rect{b.X, b.Y, b.W, b.H},
				PixelWidth: int(cgDisplayPixelsWide(id)), PixelHeight: int(cgDisplayPixelsHigh(id)),
			})
		}
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Main != b.Main {
			return a.Main
		}
		if a.Bounds.X != b.Bounds.X {
			return a.Bounds.X < b.Bounds.X
		}
		if a.Bounds.Y != b.Bounds.Y {
			return a.Bounds.Y < b.Bounds.Y
		}
		return a.UUID < b.UUID
	})
	return out, nil
}
