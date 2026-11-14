package integration

import (
	"miya/internal/desktop"
)

func desktopScroll(x, y float64, dx, dy int) (in desktop.Input) {
	in.Kind, in.X, in.Y, in.DeltaX, in.DeltaY = "scroll", x, y, dx, dy
	return
}
