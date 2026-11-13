// Package desktopcontrol reads and drives the local, logged-in desktop:
// its displays, its windows, screenshots of them, and synthesized input.
// It opens no sockets and grants no remote access; the app owns consent,
// sessions and whatever it sends elsewhere. On macOS it is all Go, through
// purego; elsewhere every function returns ErrUnsupported.
//
// Permissions are TCC's: Screen Recording for windows and screenshots,
// Accessibility for input and dialogs. Ask for them with
// plugins/permissionflow; this package only reports their absence.
package desktopcontrol

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

var (
	// ErrUnsupported is returned where the system has no implementation.
	ErrUnsupported = fmt.Errorf("desktopcontrol: %w", errors.ErrUnsupported)
	// ErrScreenRecording means the app may not capture the screen.
	ErrScreenRecording = errors.New("desktopcontrol: screen recording is not allowed")
	// ErrAccessibility means the app may not control the computer.
	ErrAccessibility = errors.New("desktopcontrol: accessibility is not allowed")
	// ErrDisplayUnavailable means the display is gone, or its ID now
	// names another screen.
	ErrDisplayUnavailable = errors.New("desktopcontrol: display unavailable")
	// ErrWindowUnavailable means the window is gone, hidden, or could not
	// be brought to the front.
	ErrWindowUnavailable = errors.New("desktopcontrol: window unavailable")
)

// Rect is a rectangle in the global display space, in points, with its
// origin at the top left of the main display.
type Rect struct{ X, Y, Width, Height float64 }

// Display is an active display. UUID identifies the screen itself: the
// system may reuse a numeric ID for another screen after one is unplugged,
// so callers keep both and pass both back.
type Display struct {
	ID     uint32
	UUID   string
	Name   string
	Main   bool
	Bounds Rect
	// PixelWidth and PixelHeight are the display mode's size in pixels.
	PixelWidth, PixelHeight int
}

// Displays lists the active displays, the main one first, then from left
// to right and top to bottom.
func Displays() ([]Display, error) { return displays() }

// Window is an on-screen window. Its ID may be reused once its app exits,
// so a window is the pair of ID and PID; callers keep and pass both.
type Window struct {
	ID       uint32
	PID      int
	App      string // the owning app's localized name
	BundleID string
	Title    string // empty when the window has none
	Bounds   Rect
	// Layer is the window level: 0 for ordinary windows, higher for
	// panels, menus and the system's alerts above them.
	Layer int
}

// InputKind is what an Input does.
type InputKind int

const (
	Move InputKind = iota + 1 // the pointer moves; no button changes
	Click
	DoubleClick
	RightClick
	Drag // presses at X, Y and releases at ToX, ToY
	Scroll
	Text // types Text, never through the clipboard
	Key  // presses and releases KeyCode with Modifiers
)

// Modifiers are the keys held during a Key, as CGEventFlags number them.
type Modifiers uint64

const (
	Shift   Modifiers = 1 << 17
	Control Modifiers = 1 << 18
	Option  Modifiers = 1 << 19
	Command Modifiers = 1 << 20
)

// Input is one synthesized action. X, Y, ToX and ToY are global points,
// as Display.Bounds and Window.Bounds are.
type Input struct {
	Kind           InputKind
	X, Y, ToX, ToY float64
	// DeltaX and DeltaY are a Scroll's distance in pixels.
	DeltaX, DeltaY int
	Text           string
	// KeyCode is a virtual key code of the Mac keyboard, such as 36 for
	// Return.
	KeyCode   uint16
	Modifiers Modifiers
}

var errInvalidInput = errors.New("desktopcontrol: invalid input")

func (in Input) validate() error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(in.X) || !finite(in.Y) || !finite(in.ToX) || !finite(in.ToY) {
		return errInvalidInput
	}
	if in.Modifiers&^(Shift|Control|Option|Command) != 0 || in.Modifiers != 0 && in.Kind != Key {
		return errInvalidInput
	}
	switch in.Kind {
	case Move, Click, DoubleClick, RightClick, Drag, Scroll, Key:
	case Text:
		if in.Text == "" || !utf8.ValidString(in.Text) {
			return errInvalidInput
		}
	default:
		return errInvalidInput
	}
	return nil
}

// Post synthesizes in as local input would arrive. It needs
// Accessibility, and returns ErrAccessibility without it.
func Post(in Input) error { return post(in, 0) }

// PostToProcess is Post, except that a Text or Key goes to the process
// pid alone. Pointer actions still land where they point.
func PostToProcess(pid int, in Input) error {
	if pid <= 0 {
		return errInvalidInput
	}
	return post(in, pid)
}

// PostToWindow is Post aimed at w, whose bounds contain the pointer
// actions' points. A pointer action needs w topmost under its point, a Text
// or Key needs w in front of its app and its app in front: when it is not,
// w is raised first, and only when its geometry names it alone. A Move
// never raises; it is skipped instead. A Text stops, with
// ErrWindowUnavailable, once w leaves the front.
func PostToWindow(w Window, in Input) error { return postToWindow(w, in) }

// DialogAction is a button of a dialog. Bounds is its frame in global
// points, zero when accessibility does not tell it.
type DialogAction struct {
	Label                    string
	Enabled, Default, Cancel bool
	Bounds                   Rect
}

// Dialog is what a window says and offers: its static labels and its
// buttons, at most eight.
type Dialog struct {
	Title, Message string
	Actions        []DialogAction
}

// ReadDialog reads w's static labels and buttons through accessibility.
// It never reads editable text or secure fields, even where accessibility
// exposes them, and skips app navigation and document contents: scroll
// areas, outlines, tables, toolbars, menu bars and web areas.
func ReadDialog(w Window) (Dialog, error) { return readDialog(w) }

// Press presses w's one enabled button titled label. An ambiguous or
// disabled label is refused with ErrWindowUnavailable.
func Press(w Window, label string) error { return press(w, label) }

// Application is an app bundle's metadata.
type Application struct {
	Name     string
	BundleID string
	Icon     []byte // PNG; nil unless asked for
}

// ApplicationInfo reads the app bundle at path: its display name, its
// identifier and, when iconSize is from 1 to 512, its icon drawn into an
// iconSize×iconSize PNG. It launches nothing.
func ApplicationInfo(path string, iconSize int) (Application, error) {
	return applicationInfo(path, iconSize)
}

// Available reports whether this system can be controlled: macOS 14 and
// later, whose ScreenCaptureKit takes screenshots.
func Available() bool { return available() }

// Windows lists the visible windows from front to back, desktop elements
// excluded. It needs Screen Recording, and never prompts for it.
func Windows() ([]Window, error) { return windows() }

// Locked reports whether the console session shows the lock screen.
func Locked() (bool, error) { return locked() }

// Wake declares local user activity, as a touch of the keyboard would: a
// sleeping display lights up, showing the desktop or the lock screen, and
// the idle timer restarts. It changes no setting and holds nothing, and
// returns once the main display is awake or about two seconds have passed.
func Wake() error { return wake() }

// PostToDisplay checks d's identity immediately before posting global input.
// Pointer coordinates are in global points, within d's bounds.
func PostToDisplay(d Display, in Input) error { return postToDisplay(d, in) }
