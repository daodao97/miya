//go:build !darwin || ios || !cgo

package desktop

import (
	"context"
	"errors"
)

func Applications() ([]Application, error)          { return nil, Authorize() }
func OpenApplication(context.Context, string) error { return Authorize() }
func RunPrivacyHelper() error                       { return Authorize() }
func Displays() ([]Display, error)                  { return nil, Authorize() }
func CaptureDisplay(Display) ([]byte, error)        { return nil, Authorize() }
func SendDisplayInput(Display, Input) error         { return Authorize() }

func Available() bool                        { return false }
func WakeDisplay() error                     { return Authorize() }
func ScreenLocked() (bool, error)            { return false, Authorize() }
func Authorize() error                       { return errors.New("Remote desktop requires macOS 14 or later") }
func Capture() ([]byte, error)               { return nil, Authorize() }
func SendInput(Input) error                  { return Authorize() }
func Windows() ([]Window, error)             { return nil, Authorize() }
func CaptureWindow(Window) ([]byte, error)   { return nil, Authorize() }
func SendWindowInput(Window, Input) error    { return Authorize() }
func WindowDialog(Window) (Dialog, error)    { return Dialog{}, Authorize() }
func PressWindowAction(Window, string) error { return Authorize() }
