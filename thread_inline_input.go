package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

const threadAttachmentURL = "miya-thread:"

type threadMentionStyle struct {
	font            ui.Font
	ink, background ui.Color
}

func mentionStyle(c *ui.Context, size float32) threadMentionStyle {
	s := threadMentionStyle{font: ui.Font{Family: c.Theme().Font, Size: size, LineHeight: 1.6}, ink: ui.Hex("#4963b8"), background: ui.Hex("#eef1fb")}
	if c.Theme().Background.R < 128 {
		s.ink, s.background = ui.Hex("#b1bfee"), ui.Hex("#2d3448")
	}
	return s
}

func (s threadMentionStyle) attachment(id int, title string) *richtext.Attachment {
	label := "@ " + clipRunes(strings.Join(strings.Fields(title), " "), 22)
	font := s.font
	font.Size *= 0.94
	font.Weight = 500
	font.LineHeight = 0
	layout := ui.ShapeText(label, font, 0)
	w, h := layout.Size()
	height := s.font.Size * 1.65
	return &richtext.Attachment{
		Name: title, URL: threadAttachmentURL + strconv.Itoa(id), Width: w + 12, Height: height,
		Draw: func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, s.background, 5)
			p.Clip(r, 5, func() { p.TextLayout(layout, r.X+6, r.Y+(r.H-h)/2, s.ink) })
		},
	}
}

func inlineThreadText(raw string, completedOnly bool, style threadMentionStyle, title func(int) string) richtext.Text {
	var runs []richtext.Run
	end := 0
	for _, match := range threadRefPattern.FindAllStringSubmatchIndex(raw, -1) {
		if completedOnly && match[1] == len(raw) {
			continue
		}
		id, err := strconv.Atoi(raw[match[4]:match[5]])
		if err != nil {
			continue
		}
		start := match[4] - 2
		runs = append(runs, richtext.Run{Text: raw[end:start]}, richtext.Run{
			Text: string(richtext.ObjectReplacement), Attributes: richtext.Attributes{
				Tag: threadAttachmentURL + strconv.Itoa(id), Attachment: style.attachment(id, title(id)),
			},
		})
		end = match[1]
	}
	runs = append(runs, richtext.Run{Text: raw[end:]})
	return richtext.TextOf(richtext.Paragraph{Runs: runs})
}

func inlineThreadRaw(text richtext.Text) string {
	var out strings.Builder
	for _, run := range text.Runs(0, text.Len()) {
		if a := run.Attributes.Attachment; a != nil && strings.HasPrefix(a.URL, threadAttachmentURL) {
			id := strings.TrimPrefix(a.URL, threadAttachmentURL)
			if _, err := strconv.Atoi(id); err == nil {
				out.WriteString("@#" + id)
				continue
			}
		}
		out.WriteString(run.Text)
	}
	return out.String()
}

func inlineThreadSpans(text richtext.Text, size float32) []ui.Span {
	var spans []ui.Span
	for _, run := range text.Runs(0, text.Len()) {
		a := run.Attributes
		span := ui.Span{Text: run.Text, Weight: a.Weight, Italic: a.Italic, Underline: a.Underline, Strikethrough: a.Strikethrough, Color: a.Color}
		if a.Code {
			span.Font = monoFont
		}
		if at := a.Attachment; at != nil {
			span.ObjectWidth, span.ObjectHeight, span.ObjectDescent = at.Width, at.Height, size*0.25
		}
		spans = append(spans, span)
	}
	return spans
}

// A document stores each reference as one editable object. The wire prompt
// remains @#id, including references inserted in the middle of a sentence.
type inlineThreadInput struct {
	doc           *richtext.Document
	raw, owner    string
	style         threadMentionStyle
	title         func(int) string
	anchor, caret int
	moved         bool
}

func (s *inlineThreadInput) setRaw(raw string) {
	if s.doc != nil && raw == s.raw && inlineThreadRaw(s.doc.Text()) == raw {
		return
	}
	text := inlineThreadText(raw, true, s.style, s.title)
	if s.doc == nil {
		s.doc = richtext.NewDocument(text)
	} else {
		// Keep completion in undo history and put the caret after its edit,
		// rather than moving it past the rest of a mid-sentence prompt.
		end := composerEditEnd(s.raw, raw)
		caret := 0
		for _, run := range text.Runs(0, text.Len()) {
			part := richtext.TextOf(richtext.Paragraph{Runs: []richtext.Run{run}})
			n := len(inlineThreadRaw(part))
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
		s.doc.Edit(func(e *richtext.Edit) { e.Replace(0, e.Text().Len(), text); e.Select(caret, caret) })
	}
	s.raw = raw
}

func (s *inlineThreadInput) sync(raw string, changed func(string)) {
	if s.doc == nil || raw != s.raw {
		s.setRaw(raw)
		return
	}
	if edited := inlineThreadRaw(s.doc.Text()); edited != raw {
		s.raw = edited
		changed(edited)
	}
}

func (s *inlineThreadInput) insert(text string) {
	a, z := s.doc.Selection()
	start, end := min(a, z), max(a, z)
	part := inlineThreadText(text, true, s.style, s.title)
	s.doc.Edit(func(e *richtext.Edit) { e.Replace(start, end, part); e.Select(start+part.Len(), start+part.Len()) })
}

func (app *chatApp) threadInput(c *ui.Context, state *inlineThreadInput, raw, owner, label, placeholder string, size, width float32, minLines, maxLines int, changed func(string)) ui.Element {
	notify := func(text string) { changed(text); c.Invalidate() }
	state.style, state.title = mentionStyle(c, size), app.referenceTitle
	if state.owner != owner {
		state.doc, state.raw, state.owner = nil, "", owner
	}
	state.moved = false
	if state.doc != nil && state.raw == raw && inlineThreadRaw(state.doc.Text()) == raw {
		a, z := state.doc.Selection()
		state.moved = a != state.anchor || z != state.caret
	}
	state.sync(raw, notify)
	// Drawing colors follow the appearance without changing the undo history.
	for _, run := range state.doc.Text().Runs(0, state.doc.Text().Len()) {
		if a := run.Attributes.Attachment; a != nil {
			id, err := strconv.Atoi(strings.TrimPrefix(a.URL, threadAttachmentURL))
			if err == nil {
				fresh := state.style.attachment(id, a.Name)
				a.Draw, a.Width, a.Height = fresh.Draw, fresh.Width, fresh.Height
			}
		}
	}
	services := c.Services()
	paste := func() {
		// File managers and image sources may also offer plain text. Read
		// attachment formats first, before falling back to that text.
		clipboard := services.ReadClipboardData(transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text)
		if !app.mobile && app.pasteAttachments(clipboard) {
			services.Invalidate()
			return
		}
		if text, err := clipboard.Read(transfer.Text); err == nil && len(text) > 0 {
			state.insert(string(text))
		}
	}
	copySelection := func(cut bool) {
		a, z := state.doc.Selection()
		if a == z {
			return
		}
		start, end := min(a, z), max(a, z)
		services.WriteClipboard(inlineThreadRaw(state.doc.Text().Slice(start, end)))
		if cut {
			state.doc.Edit(func(e *richtext.Edit) { e.ReplaceString(start, end, "", richtext.Attributes{}); e.Select(start, start) })
		}
	}
	contextMenu := func(menu *ui.Menu) {
		a, z := state.doc.Selection()
		if menu.Item("复制").Disabled(a == z).Chosen() {
			copySelection(false)
		}
		if menu.Item("剪切").Disabled(a == z).Chosen() {
			copySelection(true)
		}
		if menu.Item("粘贴").Chosen() {
			paste()
		}
		if menu.Item("全选").Chosen() {
			state.doc.SetSelection(0, state.doc.Text().Len())
		}
	}
	in := richtext.EditorBase(c, state.doc, richtext.EditorOptions{
		Label: label, Placeholder: placeholder, NoMarkdown: true, Font: state.style.font,
		OnPaste: func() bool { paste(); return true }, ContextMenu: contextMenu,
		TagStyle: func(_ string, span *ui.Span) { span.ObjectDescent = size * 0.25 },
	}).FillWidth().MinWidth(0).FocusRing(false).InputOptions(ui.InputOptions{Dismiss: ui.KeyboardDismissInteractive})
	if measured := in.Bounds().W; measured > 0 {
		width = measured
	}
	spans := inlineThreadSpans(state.doc.Text(), size)
	_, height := ui.ShapeRichText(spans, state.style.font, max(1, width)).Size()
	lineHeight := size * 1.6
	height = max(float32(minLines)*lineHeight, min(float32(maxLines)*lineHeight, height))
	in.Height(height)
	if in.Shortcut(ui.Cmd, ui.KeyC) {
		copySelection(false)
	}
	if in.Shortcut(ui.Cmd, ui.KeyX) {
		copySelection(true)
	}

	state.sync(state.raw, notify)
	state.anchor, state.caret = state.doc.Selection()
	return in
}
