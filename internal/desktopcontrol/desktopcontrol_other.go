//go:build !darwin || ios

package desktopcontrol

import "context"

func displays() ([]Display, error) { return nil, ErrUnsupported }
func available() bool              { return false }
func windows() ([]Window, error)   { return nil, ErrUnsupported }
func locked() (bool, error)        { return false, ErrUnsupported }
func wake() error                  { return ErrUnsupported }

func applicationInfo(string, int) (Application, error) { return Application{}, ErrUnsupported }

func post(in Input, _ int) error {
	if err := in.validate(); err != nil {
		return err
	}
	return ErrUnsupported
}

func postToWindow(_ Window, in Input) error { return post(in, 0) }

func readDialog(Window) (Dialog, error) { return Dialog{}, ErrUnsupported }
func press(Window, string) error        { return ErrUnsupported }

func capture(context.Context, Display, Window, CaptureOptions) ([]byte, error) {
	return nil, ErrUnsupported
}
func runPrivacy(context.Context, func() error) error { return ErrUnsupported }
func postToDisplay(Display, Input) error             { return ErrUnsupported }
