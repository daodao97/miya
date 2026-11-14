// Package nswidgets is Miya's shared look on NSGo: its theme, colors, icons
// and common controls. It replaces internal/widgets, which is written
// against MyGo, as views move over; see docs/nsgo-migration.md.
package nswidgets

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// MonoFont is the family code and terminals use.
const MonoFont = "Menlo"

// Roles Miya adds to NSGo's palette, by name in Palette.Colors. NSGo's own
// roles hold the rest: Background (canvas), Surface (bubble), SurfaceHover
// (hover), Border (line), Text (ink), TextMuted (muted), Danger, Warning
// (warnInk) and Success.
const (
	RoleSide      = "side"
	RolePanel     = "panel"
	RoleChosen    = "chosen"
	RoleEdge      = "edge" // the stronger border of popups and inputs
	RoleSubtle    = "subtle"
	RoleSelected  = "selected"
	RoleFaint     = "faint"
	RoleWarnBg    = "warnBg"
	RoleWarnLine  = "warnLine"
	RoleSendHover = "sendHover"
	RoleFailed    = "failed"
	RoleShadow    = "shadow"
	RoleLink      = "link"
	RoleAdded     = "added"
	RoleHunk      = "hunk"
	RoleCodeBg    = "codeBg"
	RoleCodeInk   = "codeInk"
)

// palette is Miya's colors for one appearance, as internal/widgets keeps
// them, so both interfaces look the same while they coexist.
type palette struct {
	canvas, side, panel, ink, muted, line, hover, chosen, bubble core.Color
	edge, subtle, selected, faint, warnBg, warnLine, warnInk     core.Color
	success, sendHover, danger, failed, shadow, link             core.Color
}

var light = palette{
	canvas: core.RGB(0xffffff), side: core.RGB(0xfafafa), panel: core.RGB(0xffffff), ink: core.RGB(0x202020), muted: core.RGB(0x858585),
	line: core.RGB(0xe5e5e5), hover: core.RGB(0xf0f0f0), chosen: core.RGB(0xeeeeee), bubble: core.RGB(0xf4f4f4),
	edge: core.RGB(0xdddddd), subtle: core.RGB(0xf7f7f7), selected: core.RGB(0xf1f2f4), faint: core.RGB(0xa3a7b3),
	warnBg: core.RGB(0xfbf6ea), warnLine: core.RGB(0xeadfc4), warnInk: core.RGB(0xb7791f),
	success: core.RGB(0x3f9a63), sendHover: core.RGB(0xdcdcdc), danger: core.RGB(0xb42318), failed: core.RGB(0xe58b84),
	shadow: core.RGBA(0x000000, 0.16), link: core.RGB(0x2563eb),
}

var dark = palette{
	canvas: core.RGB(0x1a1a1c), side: core.RGB(0x141416), panel: core.RGB(0x1f1f22), ink: core.RGB(0xececee), muted: core.RGB(0x8e8e93),
	line: core.RGB(0x2e2e32), hover: core.RGB(0x2a2a2e), chosen: core.RGB(0x2c2c30), bubble: core.RGB(0x2a2a2e),
	edge: core.RGB(0x38383c), subtle: core.RGB(0x222226), selected: core.RGB(0x2c2c30), faint: core.RGB(0x6b6e78),
	warnBg: core.RGB(0x2b2618), warnLine: core.RGB(0x4a3f22), warnInk: core.RGB(0xe0a54a),
	success: core.RGB(0x4cc38a), sendHover: core.RGB(0x3a3a3e), danger: core.RGB(0xf87171), failed: core.RGB(0xf19a93),
	shadow: core.RGBA(0x000000, 0.5), link: core.RGB(0x6ba4ff),
}

// Theme is Miya's look in both appearances. Set it at the root with
// cx.SetTheme; the appearance follows the system unless the app forces one.
func Theme() *core.Theme {
	t := *core.DefaultTheme()
	t.Light = light.apply(t.Light)
	t.Dark = dark.apply(t.Dark)
	t.Mono = MonoFont
	return &t
}

// apply sets Miya's colors over a copy of base, keeping the roles NSGo's ui
// components read that Miya does not name.
func (p palette) apply(base core.Palette) core.Palette {
	colors := make(map[string]core.Color, len(base.Colors)+20)
	for k, v := range base.Colors {
		colors[k] = v
	}
	base.Colors = colors
	base.Background, base.Surface, base.SurfaceHover, base.SurfacePressed = p.canvas, p.bubble, p.hover, p.chosen
	base.Border, base.Text, base.TextMuted = p.line, p.ink, p.muted
	// Miya's accent is its ink: the send button and primary actions are
	// the text's color, not a hue.
	base.Accent, base.AccentText = p.ink, p.canvas
	base.AccentHover, base.AccentPressed = ui.Mix(p.ink, p.canvas, 0.15), ui.Mix(p.ink, p.canvas, 0.25)
	base.Danger, base.Warning, base.Success = p.danger, p.warnInk, p.success
	for name, c := range map[string]core.Color{
		RoleSide: p.side, RolePanel: p.panel, RoleChosen: p.chosen, RoleEdge: p.edge, RoleSubtle: p.subtle,
		RoleSelected: p.selected, RoleFaint: p.faint, RoleWarnBg: p.warnBg, RoleWarnLine: p.warnLine,
		RoleSendHover: p.sendHover, RoleFailed: p.failed, RoleShadow: p.shadow, RoleLink: p.link,
		RoleAdded: core.RGB(0x8bc7a1), RoleHunk: core.RGB(0x8eb6e8), RoleCodeBg: core.RGB(0x202124), RoleCodeInk: core.RGB(0xc7cad1),
	} {
		base.SetColor(name, c)
	}
	return base
}

// Colors reads Miya's colors from the theme its scope shows. Its methods
// track the theme and appearance, so call them inside a StyleFunc,
// ContentFunc or another function the engine runs again: a change of
// appearance then restyles in place.
type Colors struct{ cx *core.Scope }

// ColorsOf returns the colors of cx's theme.
func ColorsOf(cx *core.Scope) Colors { return Colors{cx} }

func (c Colors) role(name string) core.Color {
	p := c.cx.Palette()
	v, _ := p.Color(name)
	return v
}

func (c Colors) Canvas() core.Color    { p := c.cx.Palette(); return p.Background }
func (c Colors) Ink() core.Color       { p := c.cx.Palette(); return p.Text }
func (c Colors) Muted() core.Color     { p := c.cx.Palette(); return p.TextMuted }
func (c Colors) Line() core.Color      { p := c.cx.Palette(); return p.Border }
func (c Colors) Hover() core.Color     { p := c.cx.Palette(); return p.SurfaceHover }
func (c Colors) Bubble() core.Color    { p := c.cx.Palette(); return p.Surface }
func (c Colors) Danger() core.Color    { p := c.cx.Palette(); return p.Danger }
func (c Colors) WarnInk() core.Color   { p := c.cx.Palette(); return p.Warning }
func (c Colors) Success() core.Color   { p := c.cx.Palette(); return p.Success }
func (c Colors) Side() core.Color      { return c.role(RoleSide) }
func (c Colors) Panel() core.Color     { return c.role(RolePanel) }
func (c Colors) Chosen() core.Color    { return c.role(RoleChosen) }
func (c Colors) Edge() core.Color      { return c.role(RoleEdge) }
func (c Colors) Subtle() core.Color    { return c.role(RoleSubtle) }
func (c Colors) Selected() core.Color  { return c.role(RoleSelected) }
func (c Colors) Faint() core.Color     { return c.role(RoleFaint) }
func (c Colors) WarnBg() core.Color    { return c.role(RoleWarnBg) }
func (c Colors) WarnLine() core.Color  { return c.role(RoleWarnLine) }
func (c Colors) SendHover() core.Color { return c.role(RoleSendHover) }
func (c Colors) Failed() core.Color    { return c.role(RoleFailed) }
func (c Colors) Shadow() core.Color    { return c.role(RoleShadow) }
func (c Colors) Link() core.Color      { return c.role(RoleLink) }
func (c Colors) Added() core.Color     { return c.role(RoleAdded) }
func (c Colors) Hunk() core.Color      { return c.role(RoleHunk) }
func (c Colors) CodeBg() core.Color    { return c.role(RoleCodeBg) }
func (c Colors) CodeInk() core.Color   { return c.role(RoleCodeInk) }
