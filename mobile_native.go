package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (m *mobileApp) font(size float32) float32 {
	if m.textScale > 0 {
		return size * m.textScale
	}
	return size
}

// Phone controls have a generous hit area independently of the icon size.
// Text controls grow with Dynamic Type rather than keeping a fixed height.
func (m *mobileApp) phoneButton(c *ui.Context) ui.Element {
	b := ui.ButtonBase(c).Row().AlignItems(ui.Center).Justify(ui.Start).Gap(8).
		MinWidth(44).MinHeight(44).PaddingX(10).Radius(10).TextColor(m.pal.ink).FontSize(m.font(15))
	if b.Pressed() && !b.IsDisabled() {
		b.Opacity(0.65)
	}
	return b
}

func mobileFeedback(kind mygo.HapticFeedback) {
	if runtime.GOOS == "ios" {
		_ = mygo.Haptics.Play(kind)
	}
}
