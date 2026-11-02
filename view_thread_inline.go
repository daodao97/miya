package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"github.com/egoist/mygo/ui/richtext"
)

var mentionPlaceholder = regexp.MustCompile("\ue000([0-9]+)\ue001")

// Replace only explicit reference objects; Markdown keeps ownership of code,
// lists and formatting. Titles and IDs never serve as reverse lookup keys.
func (app *chatApp) threadMessage(c *ui.Context, message Message) {
	content, tags := app.threadReferenceContent(message.Content)
	if len(tags) == 0 {
		message.Content = content
		app.markdown(c, message, false)
		return
	}
	box := ui.Column(c).MinWidth(0)
	options := ui.Local(box, "thread-markdown", func() markdown.Options { return markdown.Options{} })
	options.OnLink, options.LoadImage, options.Theme = app.md.OnLink, app.md.LoadImage, app.md.Theme
	options.Block = func(c *ui.Context, doc *parser.Document, index int) bool {
		if doc.Block(index).Kind != parser.Paragraph {
			return false
		}
		raw := strings.TrimSuffix(doc.Lines(index), "\n")
		mentions := threadMentions(raw)
		if len(mentions) == 0 {
			return false
		}
		var b strings.Builder
		end := 0
		for i, m := range mentions {
			b.WriteString(raw[end:m.Start])
			fmt.Fprintf(&b, "\ue000%d\ue001", i)
			end = m.End
		}
		b.WriteString(raw[end:])
		style := mentionStyle(c, c.Theme().FontSize)
		var spans []ui.Span
		text := richtext.ParseMarkdown(b.String())
		for _, run := range text.Runs(0, text.Len()) {
			a := run.Attributes
			base := ui.Span{Text: run.Text, Weight: a.Weight, Italic: a.Italic, Underline: a.Underline, Strikethrough: a.Strikethrough}
			if a.Code {
				base.Font = monoFont
				base.Background = c.Theme().Text.Alpha(.06)
				base.Radius = 4
			}
			end := 0
			for _, match := range mentionPlaceholder.FindAllStringSubmatchIndex(run.Text, -1) {
				i, _ := strconv.Atoi(run.Text[match[2]:match[3]])
				if i >= len(mentions) {
					continue
				}
				before := base
				before.Text = run.Text[end:match[0]]
				title := mentions[i].Title
				spans = append(spans, before, ui.Span{Text: " @ " + clipRunes(strings.Join(strings.Fields(title), " "), 22) + " ", Weight: 500, Color: style.ink, Background: style.background, Radius: 5})
				end = match[1]
			}
			base.Text = run.Text[end:]
			spans = append(spans, base)
		}
		ui.RichText(c, spans...).MinWidth(0).FontSize(c.Theme().FontSize).LineHeight(1.7).Selectable()
		return true
	}
	box.Children(func() { markdown.View(c, strings.TrimRight(content, "\n"), options) })
}
