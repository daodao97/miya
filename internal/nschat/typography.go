package nschat

import (
	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/core"
)

// The phone transcript follows the MyGo Markdown theme's 17-point body,
// with line heights of 1.5 em for prose and 1.4 em for code blocks.
const (
	phoneMessageSize       core.Points = 17
	phoneMessageLineHeight             = phoneMessageSize * 1.5
	phoneCodeSize                      = phoneMessageSize * 0.875
)

var phoneMarkdownStyles = textview.Styles{
	textview.PartRoot:          {FontSize: phoneMessageSize, LineHeight: phoneMessageLineHeight},
	textview.PartHeading1:      phoneHeading(1.6, 700),
	textview.PartHeading2:      phoneHeading(1.35, 650),
	textview.PartHeading3:      phoneHeading(1.15, 600),
	textview.PartHeading4:      phoneHeading(1, 600),
	textview.PartHeading5:      phoneHeading(0.95, 600),
	textview.PartHeading6:      phoneHeading(0.9, 600),
	textview.PartAlertTitle:    {FontSize: phoneMessageSize, LineHeight: phoneMessageLineHeight, FontWeight: 600},
	textview.PartListMarker:    {MinWidth: core.Pt(phoneMessageSize)},
	textview.PartCode:          {FontSize: phoneCodeSize},
	textview.PartCodeBlockCode: {FontSize: phoneCodeSize, LineHeight: phoneCodeSize * 1.4},
	textview.PartFootnotes:     {FontSize: phoneCodeSize, LineHeight: phoneMessageLineHeight},
}

func phoneHeading(multiplier core.Points, weight core.FontWeight) core.Style {
	size := phoneMessageSize * multiplier
	// A heading starts with the body's line spacing and grows to fit its glyphs.
	return core.Style{FontSize: size, LineHeight: max(phoneMessageLineHeight, size*1.2), FontWeight: weight}
}

func transcriptMarkdownStyles(cx *core.Scope) textview.Styles {
	if cx.Env().Desktop() {
		return nil
	}
	return phoneMarkdownStyles
}
