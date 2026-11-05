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
	"miya/internal/model"
	"miya/internal/textutil"
)

var mentionPlaceholder = regexp.MustCompile("\ue000([0-9]+)\ue001")

// Replace only explicit reference objects; Markdown keeps ownership of code,
// lists and formatting. Titles and IDs never serve as reverse lookup keys.
func (app *chatApp) threadMessage(c *ui.Context, message Message) {
	if message.Role == "user" {
		message.Content = claudeCommandText(message.Content)
	}
	content, tags := app.threadReferenceContent(message.Content)
	if len(tags) == 0 {
		message.Content = content
		app.markdown(c, message, false)
		return
	}
	box := ui.Column(c).MinWidth(0)
	options := ui.Local(box, "thread-markdown", func() markdown.Options { return markdown.Options{} })
	options.OnLink, options.LoadImage, options.Theme = app.md.OnLink, app.md.LoadImage, app.md.Theme
	options.LinkMenu = app.md.LinkMenu
	options.Block = func(c *ui.Context, doc *parser.Document, index int) bool {
		if doc.Block(index).Kind != parser.Paragraph {
			return false
		}
		raw := strings.TrimSuffix(doc.Lines(index), "\n")
		mentions := model.ThreadMentions(raw)
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
		type piece struct {
			span        ui.Span
			link, label string
		}
		var pieces []piece
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
				pieces = append(pieces, piece{span: before, link: a.Link, label: before.Text}, piece{
					span: ui.Span{Text: " @ " + textutil.ClipRunes(strings.Join(strings.Fields(title), " "), 22) + " ", Weight: 500, Color: style.ink, Background: style.background, Radius: 5},
					link: mentions[i].Ref.URL(), label: title,
				})
				end = match[1]
			}
			base.Text = run.Text[end:]
			pieces = append(pieces, piece{span: base, link: a.Link, label: base.Text})
		}
		ui.RichText(c).MinWidth(0).FontSize(c.Theme().FontSize).LineHeight(1.7).Selectable().Children(func() {
			for i, part := range pieces {
				if part.span.Text == "" {
					continue
				}
				if part.link == "" {
					ui.RichText(c, part.span)
					continue
				}
				link := ui.Link(c.Key(i), "", "").Label(part.label)
				link.Children(func() { ui.RichText(c, part.span) })
				if app.md.LinkMenu != nil {
					link.ContextMenu(func(m *ui.Menu) { app.md.LinkMenu(m, part.link) })
				}
				if link.Clicked() && app.md.OnLink != nil {
					app.md.OnLink(part.link)
				}
			}
		})
		return true
	}
	box.Children(func() { markdown.View(c, strings.TrimRight(content, "\n"), options) })
}
