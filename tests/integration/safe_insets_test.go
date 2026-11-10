//go:build !windows

package integration

import "github.com/egoist/mygo/ui"

// safeInsets is the room a phone's chrome takes, which setSafeInsets gives
// a Tester (ui.Tester.SetInsets takes it as four values).
type safeInsets struct{ Top, Right, Bottom, Left float32 }

func setSafeInsets(tt *ui.Tester, a safeInsets) { tt.SetInsets(a.Top, a.Right, a.Bottom, a.Left) }
