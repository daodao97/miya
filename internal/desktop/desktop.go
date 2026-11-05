// Package desktop captures and controls the local logged-in desktop. It does
// not open sockets or grant remote access; the caller owns consent and leases.
package desktop

import (
	"errors"
	"math"
	"unicode/utf8"
)

var ErrWindowUnavailable = errors.New("The permission window was closed, obscured, or could not be focused")
var ErrDisplayUnavailable = errors.New("The selected display is unavailable. Select another desktop.")

// UUID keeps a reconnected/replaced screen from inheriting another display's
// control session when CoreGraphics reuses a numeric display ID.
type Display struct {
	ID     uint32 `json:"id"`
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Main   bool   `json:"main"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// A window identity includes its owner: window numbers may be reused after an
// app exits. A window session must never silently become a display session.
type Window struct {
	ID       uint32 `json:"id"`
	PID      int    `json:"pid"`
	App      string `json:"app"`
	BundleID string `json:"bundleId,omitempty"`
	Title    string `json:"title"`
}

type Dialog struct {
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Actions []Action `json:"actions"`
}

type Action struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Enabled bool    `json:"enabled"`
	Cancel  bool    `json:"cancel"`
	Default bool    `json:"default"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
}

func (d Dialog) InputAction(in Input) (Action, bool) {
	for _, a := range d.Actions {
		if !a.Enabled {
			continue
		}
		if in.Kind == "click" && in.X >= a.X && in.X <= a.X+a.Width && in.Y >= a.Y && in.Y <= a.Y+a.Height && a.Width > 0 && a.Height > 0 {
			return a, true
		}
		if in.Kind == "key" && in.Modifier == "" && (in.Key == "enter" && a.Default || in.Key == "escape" && a.Cancel) {
			return a, true
		}
	}
	return Action{}, false
}

type Input struct {
	Kind     string  `json:"kind"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	ToX      float64 `json:"toX,omitempty"`
	ToY      float64 `json:"toY,omitempty"`
	DeltaX   int     `json:"deltaX,omitempty"`
	DeltaY   int     `json:"deltaY,omitempty"`
	Text     string  `json:"text,omitempty"`
	Key      string  `json:"key,omitempty"`
	Modifier string  `json:"modifier,omitempty"`
}

// Coordinates are relative to the captured display, independent of Retina
// scale and the phone's zoom. Reject malformed events before native dispatch.
func (in Input) Validate() error {
	unit := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
	if !unit(in.X) || !unit(in.Y) || !unit(in.ToX) || !unit(in.ToY) {
		return errors.New("Invalid screen coordinates")
	}
	if in.Modifier != "" && in.Modifier != "command" && in.Modifier != "control" && in.Modifier != "shift" && in.Modifier != "option" {
		return errors.New("Invalid modifier key")
	}
	if in.Modifier != "" && in.Kind != "key" {
		return errors.New("Modifier keys are only supported for keyboard actions")
	}
	switch in.Kind {
	case "click", "doubleClick", "rightClick", "drag", "move":
	case "scroll":
		if in.DeltaX < -1000 || in.DeltaX > 1000 || in.DeltaY < -1000 || in.DeltaY > 1000 {
			return errors.New("Scroll distance is too large")
		}
	case "text":
		if !utf8.ValidString(in.Text) || len(in.Text) == 0 || len(in.Text) > 4096 || in.Modifier != "" {
			return errors.New("Enter 1–4096 bytes of text at a time")
		}
	case "key":
		switch in.Key {
		case "enter", "tab", "escape", "backspace", "delete", "left", "right", "up", "down", "a", "c", "v", "z", "l":
		default:
			return errors.New("Unsupported key")
		}
	default:
		return errors.New("Unsupported desktop action")
	}
	return nil
}
