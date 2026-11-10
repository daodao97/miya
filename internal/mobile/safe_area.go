package mobile

import "github.com/egoist/mygo/ui"

// safeArea is the room the system's chrome takes from the window, which
// ui.Context.SafeArea returns as four values, kept as one for the views
// that pass it around.
type safeArea struct{ Top, Right, Bottom, Left float32 }

func safeAreaOf(c *ui.Context) safeArea {
	top, right, bottom, left := c.SafeArea()
	return safeArea{top, right, bottom, left}
}
