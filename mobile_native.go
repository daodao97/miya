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

// Authorization choices use color to distinguish an available action from
// disabled native buttons, including in dark appearance.
func (m *mobileApp) approvalButton(c *ui.Context, key, label string, primary, disabled bool) ui.Element {
	p := m.pal
	bg, fg := p.bg, p.ink
	if primary {
		bg, fg = p.link, p.bg
	}
	if disabled {
		bg, fg = p.line, p.muted
	}
	b := m.phoneButton(c.Key(key)).Disabled(disabled).MinHeight(44).Padding(10, 16).Radius(22).
		Background(bg).Border(1, p.line).Justify(ui.Center).Label(label)
	if primary && !disabled {
		b.Border(1, bg)
	}
	b.Children(func() { ui.Text(c, label).FontSize(m.font(14)).FontWeight(600).TextColor(fg) })
	return b
}

func mobileFeedback(kind mygo.HapticFeedback) {
	if runtime.GOOS == "ios" {
		_ = mygo.Haptics.Play(kind)
	}
}
