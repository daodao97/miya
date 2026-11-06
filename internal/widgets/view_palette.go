package widgets

import (
	uiruntime "miya/internal/uiruntime"
	atomic "sync/atomic"

	mygo "github.com/egoist/mygo"
	ui "github.com/egoist/mygo/ui"
)

// Colors the desktop views share beyond those in view_style.go, set with
// the rest by applyDesktopPalette.
var Border = ui.Hex("#dddddd")

var Subtle = ui.Hex("#f7f7f7")

var SelectedBg = ui.Hex("#f1f2f4")

var Faint = ui.Hex("#a3a7b3")

var WarnBg = ui.Hex("#fbf6ea")

var WarnLine = ui.Hex("#eadfc4")

var WarnInk = ui.Hex("#b7791f")

var Success = ui.Hex("#3f9a63")

var SendHover = ui.Hex("#dcdcdc")

var LinkInk = ui.Hex("#2563eb")

// desktopPalette holds the desktop's colors for one appearance.
type DesktopPalette struct {
	Canvas, side, panel, Ink, Muted, Line, Hover, chosen, bubble ui.Color
	border, Subtle, selected, faint, warnBg, warnLine, WarnInk   ui.Color
	success, sendHover, Danger, failed, shadow, link             ui.Color
}

var DesktopLight = DesktopPalette{
	Canvas: ui.Hex("#ffffff"), side: ui.Hex("#fafafa"), panel: ui.Hex("#ffffff"), Ink: ui.Hex("#202020"), Muted: ui.Hex("#858585"),
	Line: ui.Hex("#e5e5e5"), Hover: ui.Hex("#f0f0f0"), chosen: ui.Hex("#eeeeee"), bubble: ui.Hex("#f4f4f4"),
	border: ui.Hex("#dddddd"), Subtle: ui.Hex("#f7f7f7"), selected: ui.Hex("#f1f2f4"), faint: ui.Hex("#a3a7b3"),
	warnBg: ui.Hex("#fbf6ea"), warnLine: ui.Hex("#eadfc4"), WarnInk: ui.Hex("#b7791f"),
	success: ui.Hex("#3f9a63"), sendHover: ui.Hex("#dcdcdc"), Danger: ui.Hex("#b42318"), failed: ui.Hex("#e58b84"), shadow: ui.RGBA(0, 0, 0, 0.16),
	link: ui.Hex("#2563eb"),
}

var DesktopDark = DesktopPalette{
	Canvas: ui.Hex("#1a1a1c"), side: ui.Hex("#141416"), panel: ui.Hex("#1f1f22"), Ink: ui.Hex("#ececee"), Muted: ui.Hex("#8e8e93"),
	Line: ui.Hex("#2e2e32"), Hover: ui.Hex("#2a2a2e"), chosen: ui.Hex("#2c2c30"), bubble: ui.Hex("#2a2a2e"),
	border: ui.Hex("#38383c"), Subtle: ui.Hex("#222226"), selected: ui.Hex("#2c2c30"), faint: ui.Hex("#6b6e78"),
	warnBg: ui.Hex("#2b2618"), warnLine: ui.Hex("#4a3f22"), WarnInk: ui.Hex("#e0a54a"),
	success: ui.Hex("#4cc38a"), sendHover: ui.Hex("#3a3a3e"), Danger: ui.Hex("#f87171"), failed: ui.Hex("#f19a93"), shadow: ui.RGBA(0, 0, 0, 0.5),
	link: ui.Hex("#6ba4ff"),
}

// applyDesktopPalette sets the shared colors for an appearance and returns
// the theme of it, which the Markdown, editor and diff views follow.
func ApplyDesktopPalette(dark bool) *ui.Theme {
	p, t := DesktopLight, *ui.LightTheme()
	if dark {
		p, t = DesktopDark, *ui.DarkTheme()
	}
	Canvas, Side, Panel, Ink, Muted, Line = p.Canvas, p.side, p.panel, p.Ink, p.Muted, p.Line
	Hover, Chosen, Bubble, Danger, Failed, Shadow = p.Hover, p.chosen, p.bubble, p.Danger, p.failed, p.shadow
	Border, Subtle, SelectedBg, Faint = p.border, p.Subtle, p.selected, p.faint
	WarnBg, WarnLine, WarnInk, Success, SendHover = p.warnBg, p.warnLine, p.WarnInk, p.success, p.sendHover
	LinkInk = p.link
	t.Background, t.Surface, t.SurfaceHover, t.Border = p.Canvas, p.bubble, p.Hover, p.Line
	t.Text, t.TextMuted, t.Danger = p.Ink, p.Muted, p.Danger
	t.Accent, t.AccentHover, t.AccentText = p.Ink, p.Ink.Mix(p.Canvas, 0.15), p.Canvas
	t.FontSize = 13
	return &t
}

// systemDark is the system's appearance, kept by mygo.Theme's updates so a
// frame never asks the system.
var SystemDark atomic.Bool

func ThemeSource(appearance string) mygo.ThemeSource {
	switch appearance {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}

// watchSystemAppearance follows the system's appearance once the app runs.
func WatchSystemAppearance() {
	SystemDark.Store(mygo.Theme.IsDark())
	mygo.Theme.OnUpdated(func() {
		SystemDark.Store(mygo.Theme.IsDark())
		uiruntime.Invalidate()
	})
}
