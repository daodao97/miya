//go:build darwin && !ios

package desktopcontrol

import (
	"errors"
	"fmt"
	"structs"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/ebitengine/purego"
)

const (
	applicationServicesPath = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"

	cgEventLeftMouseDown    = 1
	cgEventLeftMouseUp      = 2
	cgEventRightMouseDown   = 3
	cgEventRightMouseUp     = 4
	cgEventMouseMoved       = 5
	cgEventLeftMouseDragged = 6

	cgMouseButtonLeft  = 0
	cgMouseButtonRight = 1

	cgMouseEventClickState = 1
	cgHIDEventTap          = 0
	cgScrollEventUnitPixel = 0

	// textChunk keeps each Unicode event under the system's length limit.
	textChunk = 20
)

// cgPoint is CGPoint, which CGEventCreateMouseEvent takes by value.
type cgPoint struct {
	_    structs.HostLayout
	X, Y float64
}

var (
	inputOnce sync.Once
	inputErr  error

	axIsProcessTrusted              func() bool
	cgEventCreateMouseEvent         func(source uintptr, typ uint32, at cgPoint, button uint32) uintptr
	cgEventCreateKeyboardEvent      func(source uintptr, key uint16, down bool) uintptr
	cgEventCreateScrollWheelEvent2  func(source uintptr, units, count uint32, w1, w2, w3 int32) uintptr
	cgEventSetIntegerValueField     func(ev uintptr, field uint32, v int64)
	cgEventSetFlags                 func(ev uintptr, flags uint64)
	cgEventSetLocation              func(ev uintptr, point cgPoint)
	cgEventKeyboardSetUnicodeString func(ev uintptr, n uint, s *uint16)
	cgEventPost                     func(tap uint32, ev uintptr)
	cgEventPostToPid                func(pid int32, ev uintptr)

	errEvent = errors.New("desktopcontrol: the system refused to create the event")
)

func loadInput() error {
	if err := load(); err != nil {
		return err
	}
	inputOnce.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			inputErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		as, err := purego.Dlopen(applicationServicesPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			inputErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		for _, f := range []struct {
			fn   any
			lib  uintptr
			name string
		}{
			{&axIsProcessTrusted, as, "AXIsProcessTrusted"},
			{&cgEventCreateMouseEvent, cg, "CGEventCreateMouseEvent"},
			{&cgEventCreateKeyboardEvent, cg, "CGEventCreateKeyboardEvent"},
			// The variadic CGEventCreateScrollWheelEvent can't be called
			// through purego; its fixed twin exists since macOS 13.
			{&cgEventCreateScrollWheelEvent2, cg, "CGEventCreateScrollWheelEvent2"},
			{&cgEventSetIntegerValueField, cg, "CGEventSetIntegerValueField"},
			{&cgEventSetFlags, cg, "CGEventSetFlags"},
			{&cgEventSetLocation, cg, "CGEventSetLocation"},
			{&cgEventKeyboardSetUnicodeString, cg, "CGEventKeyboardSetUnicodeString"},
			{&cgEventPost, cg, "CGEventPost"},
			{&cgEventPostToPid, cg, "CGEventPostToPid"},
		} {
			p, err := purego.Dlsym(f.lib, f.name)
			if err != nil {
				inputErr = fmt.Errorf("desktopcontrol: missing %s: %w", f.name, ErrUnsupported)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
	})
	return inputErr
}

func mouse(typ uint32, at cgPoint, button uint32, clicks int64) {
	ev := cgEventCreateMouseEvent(0, typ, at, button)
	if ev == 0 {
		return
	}
	cgEventSetIntegerValueField(ev, cgMouseEventClickState, clicks)
	cgEventPost(cgHIDEventTap, ev)
	cfRelease(ev)
}

// keys presses and releases code, or types text when it is not empty.
func keys(code uint16, text []uint16, mods Modifiers, pid int) error {
	for _, down := range []bool{true, false} {
		ev := cgEventCreateKeyboardEvent(0, code, down)
		if ev == 0 {
			return errEvent
		}
		cgEventSetFlags(ev, uint64(mods))
		if len(text) > 0 {
			cgEventKeyboardSetUnicodeString(ev, uint(len(text)), &text[0])
		}
		if pid > 0 {
			cgEventPostToPid(int32(pid), ev)
		} else {
			cgEventPost(cgHIDEventTap, ev)
		}
		cfRelease(ev)
	}
	return nil
}

func post(in Input, pid int) error { return postGuarded(in, pid, nil) }

// postGuarded is post, checking front before each keyboard event: a Text
// or Key stops once its target is no longer in front.
func postGuarded(in Input, pid int, front func() bool) error {
	if err := in.validate(); err != nil {
		return err
	}
	if err := loadInput(); err != nil {
		return err
	}
	if !axIsProcessTrusted() {
		return ErrAccessibility
	}
	at := cgPoint{X: in.X, Y: in.Y}
	switch in.Kind {
	case Move:
		mouse(cgEventMouseMoved, at, cgMouseButtonLeft, 0)
	case Click, DoubleClick, RightClick, Drag:
		down, up, button := uint32(cgEventLeftMouseDown), uint32(cgEventLeftMouseUp), uint32(cgMouseButtonLeft)
		if in.Kind == RightClick {
			down, up, button = cgEventRightMouseDown, cgEventRightMouseUp, cgMouseButtonRight
		}
		mouse(cgEventMouseMoved, at, button, 0)
		mouse(down, at, button, 1)
		if in.Kind == Drag {
			for i := 1; i <= 12; i++ {
				f := float64(i) / 12
				mouse(cgEventLeftMouseDragged, cgPoint{X: in.X + (in.ToX-in.X)*f, Y: in.Y + (in.ToY-in.Y)*f}, button, 1)
				time.Sleep(5 * time.Millisecond)
			}
			at = cgPoint{X: in.ToX, Y: in.ToY}
		}
		mouse(up, at, button, 1)
		if in.Kind == DoubleClick {
			mouse(down, at, button, 2)
			mouse(up, at, button, 2)
		}
	case Scroll:
		mouse(cgEventMouseMoved, at, cgMouseButtonLeft, 0)
		ev := cgEventCreateScrollWheelEvent2(0, cgScrollEventUnitPixel, 2, int32(in.DeltaY), int32(in.DeltaX), 0)
		if ev == 0 {
			return errEvent
		}
		// A posted mouse move may still be queued. Bind the wheel event to
		// the requested point instead of the cursor position at creation.
		cgEventSetLocation(ev, at)
		cgEventPost(cgHIDEventTap, ev)
		cfRelease(ev)
	case Key:
		if front != nil && !front() {
			return ErrWindowUnavailable
		}
		return keys(in.KeyCode, nil, in.Modifiers, pid)
	case Text:
		units := utf16.Encode([]rune(in.Text))
		for at := 0; at < len(units); {
			if front != nil && !front() {
				return ErrWindowUnavailable
			}
			n := min(textChunk, len(units)-at)
			// Keep a surrogate pair in one event.
			if last := units[at+n-1]; at+n < len(units) && last >= 0xD800 && last <= 0xDBFF {
				n--
			}
			if err := keys(0, units[at:at+n], 0, pid); err != nil {
				return err
			}
			at += n
		}
	}
	return nil
}

func postToDisplay(d Display, in Input) error {
	if err := in.validate(); err != nil {
		return err
	}
	if err := load(); err != nil {
		return err
	}
	if !displayMatches(d.ID, d.UUID) {
		return ErrDisplayUnavailable
	}
	b := cgDisplayBounds(d.ID)
	bounds := Rect{b.X, b.Y, b.W, b.H}
	if in.Kind != Key && in.Kind != Text && (!contains(bounds, cgPoint{X: in.X, Y: in.Y}) || in.Kind == Drag && !contains(bounds, cgPoint{X: in.ToX, Y: in.ToY})) {
		return errInvalidInput
	}
	return post(in, 0)
}
