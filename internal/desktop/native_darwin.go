//go:build darwin && !ios

package desktop

import (
	"context"
	"errors"
	"time"

	dc "github.com/egoist/mygo/plugins/desktopcontrol"
	"github.com/egoist/mygo/plugins/permissionflow"
)

func Available() bool { return dc.Available() }

func Authorize() error {
	if !Available() {
		return permissionError(3)
	}
	for i, p := range []permissionflow.Permission{permissionflow.ScreenRecording, permissionflow.Accessibility} {
		st, err := permissionflow.Check(p)
		if err != nil {
			return err
		}
		if st != permissionflow.Granted {
			_, _ = permissionflow.Request(context.Background(), p)
			return permissionError(i + 1)
		}
	}
	return nil
}

func desktopError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dc.ErrUnsupported):
		return permissionError(3)
	case errors.Is(err, dc.ErrScreenRecording):
		return permissionError(1)
	case errors.Is(err, dc.ErrAccessibility):
		return permissionError(2)
	case errors.Is(err, dc.ErrWindowUnavailable):
		return ErrWindowUnavailable
	case errors.Is(err, dc.ErrDisplayUnavailable):
		return ErrDisplayUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return permissionError(8)
	case errors.Is(err, dc.ErrShareableContent):
		return captureError(5, err.Error())
	case errors.Is(err, dc.ErrCapture):
		return captureError(7, err.Error())
	case errors.Is(err, dc.ErrEncoding):
		return permissionError(9)
	}
	return err
}

func ScreenLocked() (bool, error) { return dc.Locked() }
func WakeDisplay() error {
	if err := dc.Wake(); err != nil {
		return permissionError(12)
	}
	return nil
}

func Capture() ([]byte, error) {
	ds, err := dc.Displays()
	if err != nil {
		return nil, desktopError(err)
	}
	for _, d := range ds {
		if d.Main {
			return captureTarget(d, dc.Window{})
		}
	}
	return nil, permissionError(13)
}

func captureTarget(d dc.Display, w dc.Window) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := dc.CaptureOptions{MaxDimension: 1600, Quality: 0.65}
	var data []byte
	var err error
	if w.ID != 0 {
		data, err = dc.CaptureWindow(ctx, w, opts)
	} else {
		data, err = dc.CaptureDisplay(ctx, d, opts)
	}
	if err != nil {
		return nil, desktopError(err)
	}
	if len(data) > 2<<20 {
		return nil, permissionError(10)
	}
	return data, nil
}

func Windows() ([]Window, error) {
	ws, err := dc.Windows()
	if err != nil {
		return nil, desktopError(err)
	}
	out := []Window{}
	for _, w := range ws {
		if w.Bounds.Width < 80 || w.Bounds.Height < 40 {
			continue
		}
		switch w.BundleID {
		case "com.daodao.miya", "com.apple.dock", "com.apple.systemuiserver", "com.apple.controlcenter", "com.apple.notificationcenterui", "com.apple.WindowManager":
			continue
		}
		name, title := w.App, w.Title
		if name == "" {
			name = "App"
		}
		if title == "" {
			title = name
		}
		out = append(out, Window{ID: w.ID, PID: w.PID, App: name, Title: title, BundleID: w.BundleID})
		if len(out) >= 100 {
			break
		}
	}
	return out, nil
}

func nativeWindow(w Window) dc.Window { return dc.Window{ID: w.ID, PID: w.PID} }

func windowInfo(w Window) (dc.Window, error) {
	if w.ID == 0 || w.PID <= 0 {
		return dc.Window{}, ErrWindowUnavailable
	}
	ws, err := dc.Windows()
	if err != nil {
		return dc.Window{}, desktopError(err)
	}
	for _, v := range ws {
		if v.ID == w.ID && v.PID == w.PID {
			return v, nil
		}
	}
	return dc.Window{}, ErrWindowUnavailable
}

func CaptureWindow(w Window) ([]byte, error) {
	if w.ID == 0 || w.PID <= 0 {
		return nil, ErrWindowUnavailable
	}
	return captureTarget(dc.Display{}, nativeWindow(w))
}

func WindowDialog(w Window) (Dialog, error) {
	window, err := windowInfo(w)
	if err != nil {
		return Dialog{}, err
	}
	d, err := dc.ReadDialog(window)
	if err != nil {
		return Dialog{}, desktopError(err)
	}
	out := Dialog{Title: d.Title, Message: d.Message, Actions: []Action{}}
	for _, a := range d.Actions {
		b := a.Bounds
		action := Action{ID: a.Label, Label: a.Label, Enabled: a.Enabled, Cancel: a.Cancel, Default: a.Default}
		if b.Width > 0 && b.Height > 0 && window.Bounds.Width > 0 && window.Bounds.Height > 0 {
			action.X = (b.X - window.Bounds.X) / window.Bounds.Width
			action.Y = (b.Y - window.Bounds.Y) / window.Bounds.Height
			action.Width = b.Width / window.Bounds.Width
			action.Height = b.Height / window.Bounds.Height
		}
		out.Actions = append(out.Actions, action)
	}
	return out, nil
}

func PressWindowAction(w Window, label string) error {
	return desktopError(dc.Press(nativeWindow(w), label))
}

func inputAtBounds(in Input, b dc.Rect) dc.Input {
	kinds := map[string]dc.InputKind{"click": dc.Click, "doubleClick": dc.DoubleClick, "rightClick": dc.RightClick, "drag": dc.Drag, "scroll": dc.Scroll, "text": dc.Text, "key": dc.Key, "move": dc.Move}
	keys := map[string]uint16{"enter": 36, "tab": 48, "escape": 53, "backspace": 51, "delete": 117, "left": 123, "right": 124, "up": 126, "down": 125, "a": 0, "c": 8, "v": 9, "z": 6, "l": 37}
	mods := map[string]dc.Modifiers{"command": dc.Command, "control": dc.Control, "shift": dc.Shift, "option": dc.Option}
	return dc.Input{Kind: kinds[in.Kind], X: b.X + in.X*(b.Width-1), Y: b.Y + in.Y*(b.Height-1), ToX: b.X + in.ToX*(b.Width-1), ToY: b.Y + in.ToY*(b.Height-1), DeltaX: in.DeltaX, DeltaY: in.DeltaY, Text: in.Text, KeyCode: keys[in.Key], Modifiers: mods[in.Modifier]}
}

func SendInput(in Input) error { return sendInput(Window{}, Display{}, in) }
func SendWindowInput(w Window, in Input) error {
	if w.ID == 0 || w.PID <= 0 {
		return ErrWindowUnavailable
	}
	return sendInput(w, Display{}, in)
}

func sendInput(w Window, d Display, in Input) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if w.ID != 0 {
		window, err := windowInfo(w)
		if err != nil {
			return err
		}
		return desktopError(dc.PostToWindow(window, inputAtBounds(in, window.Bounds)))
	}
	display, err := displayInfo(d)
	if err != nil {
		return err
	}
	return desktopError(dc.PostToDisplay(display, inputAtBounds(in, display.Bounds)))
}
