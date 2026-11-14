package nswidgets

import (
	"sync"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/iconassets"
)

var (
	iconMu    sync.Mutex
	iconCache = map[string]*core.SVG{}
)

// SVG returns Miya's icon called name, parsed once. It panics on an icon
// that is not embedded, as a missing asset is a build mistake.
func SVG(name string) *core.SVG {
	iconMu.Lock()
	defer iconMu.Unlock()
	if s, ok := iconCache[name]; ok {
		return s
	}
	data, err := iconassets.SVG(name)
	if err != nil {
		panic(err)
	}
	s := core.MustParseSVG(data)
	iconCache[name] = s
	return s
}

// LookupSVG is SVG for a name that comes from data, such as an activity's
// icon: it reports false rather than panicking when there is no such icon.
func LookupSVG(name string) (*core.SVG, bool) {
	if _, err := iconassets.SVG(name); err != nil {
		return nil, false
	}
	return SVG(name), true
}

// IconOf returns a loader of the icon called name, for components that take
// func() *core.SVG.
func IconOf(name string) func() *core.SVG { return func() *core.SVG { return SVG(name) } }

// Icon draws Miya's icon called name at size, in tint, or in the inherited
// text color when tint is nil.
func Icon(name string, size core.Points, tint func() core.Color) core.Node {
	base := core.Style{Width: core.Pt(size), Height: core.Pt(size), FlexShrink: 0}
	if tint == nil {
		return core.Icon{SVG: SVG(name), Style: base}
	}
	return core.Icon{SVG: SVG(name), StyleFunc: func() core.Style {
		s := base
		s.Color = tint()
		return s
	}}
}

// ToolButton is a square icon button whose tooltip names it, Miya's
// toolbar button.
func ToolButton(name, label string, onPress func()) core.Node {
	return ui.IconButton{Icon: IconOf(name), Label: label, Size: 32, IconSize: 17, OnPress: onPress, TestID: "tool-" + name}
}

// Quiet is Miya's borderless button: no fill until hovered.
func Quiet(text, icon string, onPress func()) core.Node {
	b := ui.Button{Text: text, Variant: ui.ButtonGhost, OnPress: onPress}
	if icon != "" {
		b.Icon = IconOf(icon)
	}
	return b
}

// SmallButton is a small secondary button, as settings rows use.
func SmallButton(text string, disabled func() bool, onPress func()) core.Node {
	return ui.Button{Text: text, Variant: ui.ButtonSecondary, Size: ui.SizeSmall, Disabled: disabled, OnPress: onPress}
}

// MutedText is secondary text at 12 pt.
func MutedText(cx *core.Scope, content func() string) core.Node {
	colors := ColorsOf(cx)
	return core.Text{ContentFunc: content, StyleFunc: func() core.Style {
		return core.Style{FontSize: 12, Color: colors.Muted()}
	}}
}

// CardStyle is the bordered panel popovers and settings use.
func CardStyle(cx *core.Scope) func() core.Style {
	colors := ColorsOf(cx)
	return func() core.Style {
		return core.Style{Padding: core.All(14), Gap: 10, Background: colors.Panel(), BorderWidth: 1, BorderColor: colors.Line(), CornerRadius: 12}
	}
}

// WorkingLabel shows a running label with its icon, still: an animated
// label kept the window building frames while a conversation ran.
func WorkingLabel(cx *core.Scope, symbol string, content func() string) core.Node {
	colors := ColorsOf(cx)
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8},
		Children: []core.Node{
			Icon(symbol, 15, colors.Muted),
			core.Text{MaxLines: 2, ContentFunc: content, StyleFunc: func() core.Style {
				return core.Style{FontSize: 12, MinWidth: core.Pt(0), Color: colors.Muted()}
			}},
		},
	}
}
