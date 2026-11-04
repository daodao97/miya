//go:build !darwin || ios || !cgo

package desktop

import "errors"

func Available() bool                        { return false }
func WakeDisplay() error                     { return Authorize() }
func Authorize() error                       { return errors.New("远程桌面需要 macOS 14 或更新版本") }
func Capture() ([]byte, error)               { return nil, Authorize() }
func SendInput(Input) error                  { return Authorize() }
func Windows() ([]Window, error)             { return nil, Authorize() }
func CaptureWindow(Window) ([]byte, error)   { return nil, Authorize() }
func SendWindowInput(Window, Input) error    { return Authorize() }
func WindowDialog(Window) (Dialog, error)    { return Dialog{}, Authorize() }
func PressWindowAction(Window, string) error { return Authorize() }
