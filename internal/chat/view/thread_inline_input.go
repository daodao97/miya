package view

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

type ThreadMentionStyle struct {
	Font            ui.Font
	ink, background ui.Color
}

func MentionStyle(c *ui.Context, size float32) ThreadMentionStyle {
	s := ThreadMentionStyle{Font: ui.Font{Family: c.Theme().Font, Size: size, LineHeight: 1.6}, ink: ui.Hex("#4963b8"), background: ui.Hex("#eef1fb")}
	if c.Theme().Background.R < 128 {
		s.ink, s.background = ui.Hex("#b1bfee"), ui.Hex("#2d3448")
	}
	return s
}

func (s ThreadMentionStyle) attachment(ref model.ThreadRef, title string) *richtext.Attachment {
	return s.tagAttachment(ref.URL(), title, "@ ")
}

func (s ThreadMentionStyle) tagAttachment(uri, title, prefix string) *richtext.Attachment {
	label := prefix + textutil.ClipRunes(strings.Join(strings.Fields(title), " "), 22)
	font := s.Font
	font.Size *= 0.94
	font.Weight = 500
	font.LineHeight = 0
	layout := ui.ShapeText(label, font, 0)
	w, h := layout.Size()
	height := s.Font.Size * 1.65
	return &richtext.Attachment{
		Name: title, URL: uri, Width: w + 12, Height: height,
		Draw: func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, s.background, 5)
			p.Clip(r, 5, func() { p.TextLayout(layout, r.X+6, r.Y+(r.H-h)/2, s.ink) })
		},
	}
}

func InlineThreadText(raw string, _ bool, style ThreadMentionStyle, title func(model.ThreadRef) string) richtext.Text {
	var runs []richtext.Run
	end := 0
	for _, m := range model.Mentions(raw) {
		name := m.Part.TitleSnapshot
		var attachment *richtext.Attachment
		uri := ""
		if m.Part.Type == "thread_ref" {
			if name == "" && title != nil {
				name = title(*m.Part.Ref)
			}
			uri = m.Part.Ref.URL()
			attachment = style.attachment(*m.Part.Ref, name)
		} else {
			if name == "" {
				name = appmodel.AgentName(m.Part.Agent)
			}
			uri = model.AgentURL(m.Part.Agent)
			attachment = style.tagAttachment(uri, name, "# ")
		}
		runs = append(runs, richtext.Run{Text: raw[end:m.Start]}, richtext.Run{
			Text: string(richtext.ObjectReplacement), Attributes: richtext.Attributes{
				Tag: uri, Attachment: attachment,
			},
		})
		end = m.End
	}
	runs = append(runs, richtext.Run{Text: raw[end:]})
	return richtext.TextOf(richtext.Paragraph{Runs: runs})
}

func InlineThreadRaw(text richtext.Text) string {
	var out strings.Builder
	for _, run := range text.Runs(0, text.Len()) {
		if a := run.Attributes.Attachment; a != nil {
			if run.Attributes.Tag == ComposerCommandTag {
				out.WriteString(a.Name)
				continue
			}
			if ref, ok := model.ParseThreadURL(a.URL); ok {
				out.WriteString(model.ReferenceText(ref, a.Name))
				continue
			}
			if agent, ok := model.ParseAgentURL(a.URL); ok {
				out.WriteString(composer.AgentReferenceText(agent, a.Name))
				continue
			}
		}
		out.WriteString(run.Text)
	}
	return out.String()
}

func InlineThreadSpans(text richtext.Text, size float32) []ui.Span {
	var spans []ui.Span
	for _, run := range text.Runs(0, text.Len()) {
		a := run.Attributes
		span := ui.Span{Text: run.Text, Weight: a.Weight, Italic: a.Italic, Underline: a.Underline, Strikethrough: a.Strikethrough, Color: a.Color}
		if a.Code {
			span.Font = widgets.MonoFont
		}
		if at := a.Attachment; at != nil {
			span.ObjectWidth, span.ObjectHeight, span.ObjectDescent = at.Width, at.Height, size*0.25
		}
		spans = append(spans, span)
	}
	return spans
}

// A document stores each reference as one editable object. The wire prompt
// preserves the stable identity and title snapshot at its original position.
type InlineThreadInput struct {
	InputHook     func(ui.InputEvent) bool
	EditMenu      func(*ui.Menu)
	Commands      []model.ComposerChoice
	Doc           *richtext.Document
	Raw, Owner    string
	Style         ThreadMentionStyle
	Title         func(model.ThreadRef) string
	anchor, caret int
	Moved         bool
}

func (s *InlineThreadInput) SetRaw(raw string) {
	if s.Doc != nil && raw == s.Raw && InlineThreadRaw(s.Doc.Text()) == raw {
		return
	}
	text := s.Text(raw)
	if s.Doc == nil {
		s.Doc = richtext.NewDocument(text)
	} else {
		// Keep completion in undo history and put the caret after its edit,
		// rather than moving it past the rest of a mid-sentence prompt.
		end := composer.ComposerEditEnd(s.Raw, raw)
		caret := 0
		for _, run := range text.Runs(0, text.Len()) {
			part := richtext.TextOf(richtext.Paragraph{Runs: []richtext.Run{run}})
			n := len(InlineThreadRaw(part))
			if end < n {
				if run.Attributes.Attachment != nil {
					if end > 0 {
						caret++
					}
				} else {
					caret += utf8.RuneCountInString(run.Text[:max(0, end)])
				}
				break
			}
			caret += utf8.RuneCountInString(run.Text)
			end -= n
		}
		s.Doc.Edit(func(e *richtext.Edit) { e.Replace(0, e.Text().Len(), text); e.Select(caret, caret) })
	}
	s.Raw = raw
}

func (s *InlineThreadInput) Sync(raw string, changed func(string)) {
	if s.Doc == nil || raw != s.Raw {
		s.SetRaw(raw)
		return
	}
	if edited := InlineThreadRaw(s.Doc.Text()); edited != raw {
		s.Raw = edited
		changed(edited)
	}
}

func (s *InlineThreadInput) insert(text string) {
	a, z := s.Doc.Selection()
	start, end := min(a, z), max(a, z)
	part := InlineThreadText(text, true, s.Style, s.Title)
	if start == 0 {
		part = s.Text(text)
	}
	s.Doc.Edit(func(e *richtext.Edit) { e.Replace(start, end, part); e.Select(start+part.Len(), start+part.Len()) })
}

// InsertText inserts dictated text at the selection, retaining reference
// attachments and a single undoable edit.
func (s *InlineThreadInput) InsertText(text string) string {
	s.insert(text)
	s.Raw = InlineThreadRaw(s.Doc.Text())
	return s.Raw
}
