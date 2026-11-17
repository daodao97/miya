//go:build !windows

package integration

import "github.com/egoist/mygo/ui"

// safeInsets is the room a phone's chrome takes, which setSafeInsets gives
// a Tester (ui.Tester.SetInsets takes it as four values), whose view pads by
// it itself, as Miya's windows do (mygo.WindowOptions.ExtendIntoSafeArea).
type safeInsets struct{ Top, Right, Bottom, Left float32 }

func setSafeInsets(tt *ui.Tester, a safeInsets) {
	tt.SetExtendIntoSafeArea(true)
	tt.SetInsets(a.Top, a.Right, a.Bottom, a.Left)
}
