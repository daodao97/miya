package main

import (
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Colors the desktop views share beyond those in view_style.go, set with
// the rest by applyDesktopPalette.
var (
	border     = ui.Hex("#dddddd") // popups and dialogs
	subtle     = ui.Hex("#f7f7f7") // soft cards on the page
	selectedBg = ui.Hex("#f1f2f4") // a chosen option
	faint      = ui.Hex("#a3a7b3") // secondary icons and labels
	warnBg     = ui.Hex("#fbf6ea")
	warnLine   = ui.Hex("#eadfc4")
	warnInk    = ui.Hex("#b7791f")
	success    = ui.Hex("#3f9a63")
	sendHover  = ui.Hex("#dcdcdc")
	// linkInk colors links in messages: the accent stays ink, so links
	// need a color of their own to stand out from the text.
	linkInk = ui.Hex("#2563eb")
)

// desktopPalette holds the desktop's colors for one appearance.
type desktopPalette struct {
	canvas, side, panel, ink, muted, line, hover, chosen, bubble ui.Color
	border, subtle, selected, faint, warnBg, warnLine, warnInk   ui.Color
	success, sendHover, danger, failed, shadow, link             ui.Color
}

var (
	desktopLight = desktopPalette{
		canvas: ui.Hex("#ffffff"), side: ui.Hex("#fafafa"), panel: ui.Hex("#ffffff"), ink: ui.Hex("#202020"), muted: ui.Hex("#858585"),
		line: ui.Hex("#e5e5e5"), hover: ui.Hex("#f0f0f0"), chosen: ui.Hex("#eeeeee"), bubble: ui.Hex("#f4f4f4"),
		border: ui.Hex("#dddddd"), subtle: ui.Hex("#f7f7f7"), selected: ui.Hex("#f1f2f4"), faint: ui.Hex("#a3a7b3"),
		warnBg: ui.Hex("#fbf6ea"), warnLine: ui.Hex("#eadfc4"), warnInk: ui.Hex("#b7791f"),
		success: ui.Hex("#3f9a63"), sendHover: ui.Hex("#dcdcdc"), danger: ui.Hex("#b42318"), failed: ui.Hex("#e58b84"), shadow: ui.RGBA(0, 0, 0, 0.16),
		link: ui.Hex("#2563eb"),
	}
	desktopDark = desktopPalette{
		canvas: ui.Hex("#1a1a1c"), side: ui.Hex("#141416"), panel: ui.Hex("#1f1f22"), ink: ui.Hex("#ececee"), muted: ui.Hex("#8e8e93"),
		line: ui.Hex("#2e2e32"), hover: ui.Hex("#2a2a2e"), chosen: ui.Hex("#2c2c30"), bubble: ui.Hex("#2a2a2e"),
		border: ui.Hex("#38383c"), subtle: ui.Hex("#222226"), selected: ui.Hex("#2c2c30"), faint: ui.Hex("#6b6e78"),
		warnBg: ui.Hex("#2b2618"), warnLine: ui.Hex("#4a3f22"), warnInk: ui.Hex("#e0a54a"),
		success: ui.Hex("#4cc38a"), sendHover: ui.Hex("#3a3a3e"), danger: ui.Hex("#f87171"), failed: ui.Hex("#f19a93"), shadow: ui.RGBA(0, 0, 0, 0.5),
		link: ui.Hex("#6ba4ff"),
	}
)

// applyDesktopPalette sets the shared colors for an appearance and returns
// the theme of it, which the Markdown, editor and diff views follow.
func applyDesktopPalette(dark bool) *ui.Theme {
	p, t := desktopLight, *ui.LightTheme()
	if dark {
		p, t = desktopDark, *ui.DarkTheme()
	}
	canvas, side, panel, ink, muted, line = p.canvas, p.side, p.panel, p.ink, p.muted, p.line
	hover, chosen, bubble, danger, failed, shadow = p.hover, p.chosen, p.bubble, p.danger, p.failed, p.shadow
	border, subtle, selectedBg, faint = p.border, p.subtle, p.selected, p.faint
	warnBg, warnLine, warnInk, success, sendHover = p.warnBg, p.warnLine, p.warnInk, p.success, p.sendHover
	linkInk = p.link
	t.Background, t.Surface, t.SurfaceHover, t.Border = p.canvas, p.bubble, p.hover, p.line
	t.Text, t.TextMuted, t.Danger = p.ink, p.muted, p.danger
	t.Accent, t.AccentHover, t.AccentText = p.ink, p.ink.Mix(p.canvas, 0.15), p.canvas
	t.FontSize = 13
	return &t
}

// systemDark is the system's appearance, kept by mygo.Theme's updates so a
// frame never asks the system.
var systemDark atomic.Bool

func themeSource(appearance string) mygo.ThemeSource {
	switch appearance {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}

// watchSystemAppearance follows the system's appearance once the app runs.
func watchSystemAppearance() {
	systemDark.Store(mygo.Theme.IsDark())
	mygo.Theme.OnUpdated(func() {
		systemDark.Store(mygo.Theme.IsDark())
		invalidate()
	})
}

func (app *chatApp) wantsDark() bool {
	switch app.appearance {
	case "dark":
		return true
	case "light":
		return false
	}
	return systemDark.Load()
}

// applyAppearance switches the palette when the wanted appearance changes.
func (app *chatApp) applyAppearance() {
	if dark := app.wantsDark(); !app.themed || dark != app.dark {
		app.theme = applyDesktopPalette(dark)
		app.md.Theme = &markdown.Theme{Link: linkInk}
		app.dark, app.themed = dark, true
	}
}

func (app *chatApp) setAppearance(v string) {
	v = normalizeAppearance(v)
	app.appearance = v
	persistSetting("appearance", v)
	if currentWindow() != nil {
		mygo.Theme.SetSource(themeSource(v))
		systemDark.Store(mygo.Theme.IsDark())
	}
	invalidate()
}

// windowBackground fills a new window until its first frame paints.
func windowBackground(app *chatApp) string {
	if app.wantsDark() {
		return "#1a1a1c"
	}
	return "#ffffff"
}

// appearanceSettings chooses light, dark or the system's appearance.
func (app *chatApp) appearanceSettings(c *ui.Context) {
	title := tr("Appearance")
	settingRow(c, title, "", func() {
		ids := []string{"", "light", "dark"}
		i := 0
		for j, id := range ids {
			if app.appearance == id {
				i = j
			}
		}
		before := i
		ui.Segmented(c.Key("appearance"), &i,
			tr("System"), tr("Light"), tr("Dark")).Label(title)
		if i != before {
			app.setAppearance(ids[i])
		}
	})
}
