package main

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"miya/internal/textutil"
)

const composerCommandTag = "composer-command"

func (s *inlineThreadInput) text(raw string) richtext.Text {
	text := inlineThreadText(raw, true, s.style, s.title)
	// A delimiter confirms the command. Partial names and paths stay editable.
	end := strings.IndexAny(raw, " \t\r\n")
	if end <= 1 || raw[0] != '/' {
		return text
	}
	for _, choice := range s.commands {
		if raw[1:end] == choice.Name {
			return text.ReplaceString(0, utf8.RuneCountInString(raw[:end]), string(richtext.ObjectReplacement), richtext.Attributes{
				Tag: composerCommandTag, Attachment: s.style.commandAttachment(raw[:end]),
			})
		}
	}
	return text
}

func (s threadMentionStyle) commandAttachment(name string) *richtext.Attachment {
	font := s.font
	font.Family, font.Weight, font.LineHeight = monoFont, 600, 0
	font.Size *= 0.94
	layout := ui.ShapeText(textutil.ClipRunes(name, 24), font, 0)
	w, h := layout.Size()
	return &richtext.Attachment{
		Name: name, Width: w + 16, Height: s.font.Size * 1.8,
		Draw: func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, s.background, 6)
			p.Clip(r, 6, func() { p.TextLayout(layout, r.X+8, r.Y+(r.H-h)/2, s.ink) })
		},
	}
}

func (s *inlineThreadInput) commandHint() string {
	if s.doc == nil || s.doc.Text().Len() == 0 {
		return ""
	}
	a := s.doc.Text().AttributesAt(0)
	if a.Tag != composerCommandTag || a.Attachment == nil {
		return ""
	}
	name := strings.TrimPrefix(a.Attachment.Name, "/")
	for _, choice := range s.commands {
		if choice.Name == name {
			return textutil.FirstNonEmpty(choice.ArgumentHint, choice.Detail)
		}
	}
	return ""
}
