package conversation

import (
	textutil "miya/internal/textutil"
	widgets "miya/internal/widgets"
	strings "strings"
	utf8 "unicode/utf8"

	ui "github.com/egoist/mygo/ui"
	richtext "github.com/egoist/mygo/ui/richtext"
)

const ComposerCommandTag = "composer-command"

func (s *InlineThreadInput) Text(raw string) richtext.Text {
	text := InlineThreadText(raw, true, s.Style, s.Title)
	// A delimiter confirms the command. Partial names and paths stay editable.
	end := strings.IndexAny(raw, " \t\r\n")
	if end <= 1 || raw[0] != '/' {
		return text
	}
	for _, choice := range s.Commands {
		if raw[1:end] == choice.Name {
			return text.ReplaceString(0, utf8.RuneCountInString(raw[:end]), string(richtext.ObjectReplacement), richtext.Attributes{
				Tag: ComposerCommandTag, Attachment: s.Style.commandAttachment(raw[:end]),
			})
		}
	}
	return text
}

func (s ThreadMentionStyle) commandAttachment(name string) *richtext.Attachment {
	font := s.Font
	font.Family, font.Weight, font.LineHeight = widgets.MonoFont, 600, 0
	font.Size *= 0.94
	layout := ui.ShapeText(textutil.ClipRunes(name, 24), font, 0)
	w, h := layout.Size()
	return &richtext.Attachment{
		Name: name, Width: w + 16, Height: s.Font.Size * 1.8,
		Draw: func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, s.background, 6)
			p.Clip(r, 6, func() { p.TextLayout(layout, r.X+8, r.Y+(r.H-h)/2, s.ink) })
		},
	}
}

func (s *InlineThreadInput) CommandHint() string {
	if s.Doc == nil || s.Doc.Text().Len() == 0 {
		return ""
	}
	a := s.Doc.Text().AttributesAt(0)
	if a.Tag != ComposerCommandTag || a.Attachment == nil {
		return ""
	}
	name := strings.TrimPrefix(a.Attachment.Name, "/")
	for _, choice := range s.Commands {
		if choice.Name == name {
			return textutil.FirstNonEmpty(choice.ArgumentHint, choice.Detail)
		}
	}
	return ""
}
