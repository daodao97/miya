package main

import (
	"fmt"
	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/subagent"
	"miya/internal/textutil"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"github.com/egoist/mygo/ui/richtext"
)

// conversationHost is the state a conversation renderer reads. It owns no agent or desktop state.
type conversationHost interface {
	Current() Thread
	Revision() int
	Approvals() []codex.Approval
	SelectThread(Thread)
}

// conversationView owns presentation state shared by desktop and phone.
type conversationView struct {
	c                conversationHost
	theme            *ui.Theme
	md               markdown.Options
	mobile           bool
	imagePreview     func(string) ui.ImageSource
	referenceThread  func(ThreadRef) (Thread, bool)
	resolveTitle     func(ThreadRef) string
	onRetry          func(retryRequest)
	onHandoff        func()
	workingAccessory func(*ui.Context)
	desktopAccessory func(*ui.Context, Thread)
	userGroup        func(*ui.Context, messageGroup)
	memoryNotice     func(*ui.Context, *MemoryNotice)
	needsReadFrame   func(Thread, *ui.ListState, int) bool
	pasteAttachments func(transfer.Data) bool
	setNotice        func(string)
	lists            map[int]*ui.ListState
	groups           groupCache
	copied, open     map[int]bool
}

func newConversationView(host conversationHost) *conversationView {
	return &conversationView{c: host, theme: appTheme(), lists: map[int]*ui.ListState{}, copied: map[int]bool{}, open: map[int]bool{}}
}
func (app *conversationView) referenceTitle(ref ThreadRef) string {
	if app.referenceThread != nil {
		if t, ok := app.referenceThread(ref); ok {
			return threadRefTitle(t)
		}
	}
	if app.resolveTitle != nil {
		return app.resolveTitle(ref)
	}
	return tr("Session unavailable")
}
func (app *conversationView) listFor(id int) *ui.ListState {
	if s := app.lists[id]; s != nil {
		return s
	}
	s := &ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth}
	app.lists[id] = s
	return s
}

func (app *conversationView) messageList(c *ui.Context, t Thread) {
	c = c.Key(t.ID)
	groups := app.groups.ofThread(t, app.c.Revision())
	status, completedGroup := currentTurnStatus(t, groups)
	state := app.listFor(t.ID)
	state.Key = func(i int) any { return messageViewKey(groups[i].Messages[0]) }
	if t.Busy {
		// The elapsed time of the running turn counts up.
		c.After(time.Second)
	}
	list := ui.List(c, state, len(groups), func(i int) {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(744).Children(func() {
				app.groupViewWithFooter(c, groups[i], i != completedGroup)
			})
		})
	}).Grow(1).MinHeight(0).Padding(app.listPadding()).Gap(app.listPadding()).Children(func() {
		if len(t.Messages) == 0 && !t.Loading && t.Status != "" && !t.Busy {
			ui.Column(c).AlignItems(ui.Center).Gap(8).Margin(ui.Auto).Children(func() {
				mutedText(c, tr("Unable to load conversation"))
				mutedText(c, t.Status)
				if quiet(c.Key("retry")).Children(func() { mutedText(c, tr("Retry")) }).Clicked() {
					app.c.SelectThread(app.c.Current())
				}
			})
		} else if t.Loading && len(t.Messages) == 0 {
			ui.Row(c).Gap(8).Margin(ui.Auto).Children(func() {
				ui.Spinner(c)
				mutedText(c, tr("Loading conversation…"))
			})
		} else if len(t.Messages) > 0 {
			mutedText(c, tr("No visible messages (reasoning-only or unsupported items)")).Margin(ui.Auto)
		}
	})
	messageScrollbars(c, list)
	if app.mobile {
		// Claim whitespace before the drawer's Draggable ancestor, but keep
		// child buttons as the innermost click targets. A blanket HandleInput
		// would intercept their presses before ButtonBase could handle them.
		_ = list.Clicked()
	}
	// A completed result needs one frame on screen before it is acknowledged.
	// Request that frame even if finishing the turn left the window idle.
	if !app.mobile && app.needsReadFrame != nil && app.needsReadFrame(t, state, len(groups)) {
		if w := currentWindow(); w != nil && w.IsFocused() && !w.IsMinimized() {
			c.After(0)
		}
	}
	if !state.AtEnd() && len(groups) > 0 {
		ui.Row(c).Justify(ui.Center).Margin(-44, 0, 8, 0).Children(func() {
			jump := quiet(c.Key("latest")).Background(canvas).Border(1, line).Radius(16).Shadow(0, 2, 8, 0, shadow)
			jump.Children(func() { mutedText(c, tr("↓ Latest")) })
			if jump.Clicked() {
				state.ScrollToEnd()
			}
		})
	}
	if status != "" || completedGroup >= 0 {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, app.listPadding(), 6, app.listPadding()).Children(func() {
			row := ui.Row(c.Key("turn-status")).FillWidth().MaxWidth(744).MinHeight(44).Gap(8).AlignItems(ui.Center)
			if app.mobile {
				row.Gap(6)
			}
			row.Children(func() {
				phoneHint, desktopHint := "", false
				if label := threadReconnectLabel(t); t.Busy && label != "" {
					ui.Row(c.Key("reconnecting")).Gap(8).AlignItems(ui.Center).Label(label).Children(func() {
						ui.Spinner(c).FontSize(13).TextColor(warnInk)
						ui.Text(c, label).FontSize(12).TextColor(warnInk).Tooltip(t.Status)
					})
				} else if status != "" {
					label := workingLabel(c.Key("working"), "clock", status).MinWidth(0).Shrink(1)
					if t.Busy {
						label.Tooltip(tr("Average output speed for this turn, including wait time."))
					} else if completedGroup >= 0 {
						label.Tooltip(formatMessageTimestamp(groups[completedGroup].Timestamp))
					}
					if hint := stalledHint(t, len(app.c.Approvals()) > 0, time.Now()); t.Busy && hint != "" {
						if app.mobile {
							phoneHint = hint
						} else {
							desktopHint = true
							ui.Text(c, "· "+hint).Grow(1).MinWidth(0).FontSize(12).TextColor(warnInk)
						}
					}
				}
				if completedGroup >= 0 {
					responseDiffStats(c.Key("diff-stats"), groups[completedGroup].DiffStats)
					app.responseActions(c.Key("response-actions"), groups[completedGroup])
				}
				// The phone's short hint and the Agent tasks toggle keep to the
				// right edge; a desktop hint takes the free space before them.
				trailing := ui.Row(c.Key("trailing")).MinWidth(0).Shrink(0).Gap(8).Justify(ui.End).AlignItems(ui.Center)
				if desktopHint {
					trailing.Shrink(0)
				} else {
					trailing.Grow(1)
				}
				trailing.Children(func() {
					if phoneHint != "" {
						ui.Text(c, tr("No response yet")).Shrink(0).SingleLine().FontSize(12).TextColor(warnInk).Tooltip(phoneHint)
					}
					if app.workingAccessory != nil {
						app.workingAccessory(c.Key("accessory"))
					} else if !app.mobile {
						if app.desktopAccessory != nil {
							app.desktopAccessory(c.Key("accessory"), t)
						}
					}
				})
			})
		})
	}
}

func (app *conversationView) turnStatusVisible(t Thread) bool {
	label, completedGroup := currentTurnStatus(t, app.groups.ofThread(t, app.c.Revision()))
	return label != "" || completedGroup >= 0
}

func (app *conversationView) groupView(c *ui.Context, g messageGroup) {
	app.groupViewWithFooter(c, g, true)
}

func (app *conversationView) groupViewWithFooter(c *ui.Context, g messageGroup, inlineFooter bool) {
	if g.User {
		if app.userGroup != nil {
			app.userGroup(c, g)
		} else {
			app.messageView(c, g.Messages[0], false)
		}
		return
	}
	group := ui.Column(c).FillWidth().Gap(12)
	hovered := group.Hovered()
	group.Children(func() {
		if g.ShowTimestamp {
			ui.Text(c, formatMessageTimestamp(g.Timestamp)).AlignSelf(ui.Start).TextColor(muted).FontSize(11)
		}
		for _, run := range activityRuns(g.Messages) {
			app.activityRunView(c.Key(messageViewKey(run.Messages[0])), run, g.Streaming)
		}
		if g.Streaming {
			return
		}
		if !inlineFooter {
			return
		}
		size := float32(28)
		if app.mobile {
			size = 44
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Height(size).Children(func() {
			elapsed := turnElapsedLabel(g.Messages, false)
			if elapsed != "" {
				ui.Text(c, elapsed).TextColor(muted).FontSize(11).Tooltip(formatMessageTimestamp(g.Timestamp))
			}
			if stats := g.DiffStats; stats.Added > 0 || stats.Deleted > 0 {
				ui.Row(c.Key("diff-stats")).Gap(4).AlignItems(ui.Center).Children(func() {
					if elapsed != "" {
						ui.Text(c, "·").TextColor(muted).FontSize(11)
					}
					responseDiffStats(c, stats)
				})
			}
			copied := app.copied[g.ID]
			if !app.mobile && !hovered && !copied {
				return
			}
			app.responseActions(c.Key("actions"), g)
		})
	})
}

func (app *conversationView) responseActions(c *ui.Context, g messageGroup) {
	size, gap := float32(28), float32(8)
	copyAlign, handoffAlign := ui.Center, ui.Center
	if app.mobile {
		size, gap, copyAlign, handoffAlign = 44, 0, ui.End, ui.Start
	}
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(gap).Children(func() {
		name := "copy"
		if app.copied[g.ID] {
			name = "check"
		}
		b := quiet(c.Key("copy")).Size(size, size).PaddingX(6).Justify(copyAlign).Label(tr("Copy response")).Tooltip(tr("Copy response"))
		b.Children(func() { icon(c, name, 15, muted) })
		if b.Clicked() {
			c.WriteClipboard(messageGroupText(g))
			app.copied[g.ID] = true
		}
		h := quiet(c.Key("handoff")).Size(size, size).PaddingX(6).Justify(handoffAlign).Label(tr("Hand off to a new conversation")).Tooltip(tr("Hand off to a new conversation"))
		h.Children(func() { icon(c, "arrow-right", 15, muted) })
		if h.Clicked() {
			if app.onHandoff != nil {
				app.onHandoff()
			}
		}
	})
}

func (app *conversationView) activityRunView(c *ui.Context, run activityRun, streaming bool) {
	if !run.Activity {
		app.messageView(c, run.Messages[0], streaming)
		return
	}
	if len(run.Messages) == 1 {
		app.messageView(c.Key(messageViewKey(run.Messages[0])), run.Messages[0], streaming)
		return
	}
	expanded := app.open[-run.ID]
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		header := quiet(c.Key("header")).PaddingX(0).AlignSelf(ui.Start)
		// Sub-agents inside a collapsed run still show that they work.
		agents := subagent.Latest(run.Messages)
		header.Children(func() {
			icon(c, "stack-2", 14, muted)
			if subagent.AnyActive(agents) {
				ui.Spinner(c).FontSize(12)
			}
			label := fmt.Sprintf("%d activities", len(run.Messages))
			if s := subagent.Counts(agents); s != "" {
				label += " · " + s
			}
			mutedText(c, label)
			app.chevron(c, expanded)
		})
		if header.Clicked() {
			app.open[-run.ID] = !expanded
		}
		// Collapsed activity details are not built at all.
		if expanded {
			for _, m := range run.Messages {
				app.messageView(c.Key(messageViewKey(m)), m, streaming)
			}
		}
	})
}

func (app *conversationView) chevron(c *ui.Context, open bool) {
	ch := icon(c, "chevron-down", 14, muted)
	ch.Rotate(ch.Animate("open", map[bool]float32{false: 0, true: 180}[open], 150*time.Millisecond))
}

func (app *conversationView) messageView(c *ui.Context, m Message, streaming bool) {
	switch m.Role {
	case "memory":
		if app.memoryNotice != nil {
			app.memoryNotice(c, m.Memory)
		} else if m.Memory != nil && m.Memory.Explicit {
			ui.Text(c, tr("Miya memory changed. View it on the memory page; edit on your desktop.")).FontSize(12).TextColor(muted)
		}
		return
	case "error":
		ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(16).Gap(10).Border(1, line).Radius(16).Children(func() {
			icon(c, "alert-circle", 16, warnInk)
			ui.Text(c, nativeText(tr(m.Content))).Grow(1).MinWidth(0).Selectable()
			if t := app.c.Current(); retryableError(t, m) {
				size := float32(28)
				if app.mobile {
					size = 44
				}
				b := quiet(c.Key("retry-response")).Size(size, size).Shrink(0).PaddingX(6).Justify(ui.Center).
					Disabled(t.Busy || t.Loading).Label(tr("Retry response")).Tooltip(tr("Retry response"))
				b.Children(func() { icon(c, "refresh", 16, muted) })
				if b.Clicked() {
					if app.onRetry != nil {
						app.onRetry(retryRequest{ThreadID: t.ID, MessageID: m.ID})
					}
				}
			}
		})
		return
	case "user":
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if len(m.Images) > 0 {
				ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
					for _, src := range m.Images {
						loader := cachedImage
						if app.imagePreview != nil {
							loader = app.imagePreview
						}
						if img := loader(src); img != nil {
							ui.Image(c.Key(src), img).Size(80, 80).Fit(ui.Cover).Radius(8).Label(tr("Image preview"))
						} else {
							ui.Box(c.Key(src)).Size(80, 80).Radius(8).Background(bubble).Label(tr("Image preview")).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								icon(c, "file", 24, muted)
							})
						}
					}
				})
			}
			// Short prompts stay compact; long ones wrap within a bounded bubble.
			prompt := ui.Column(c).AlignSelf(ui.End).MaxWidth(560).MinWidth(0).Background(bubble).Radius(16).Padding(14).Gap(8)
			if app.mobile {
				prompt.MaxWidthPercent(100)
			}
			prompt.Children(func() {
				app.threadMessage(c, m)
			})
		})
		return
	case "assistant":
		app.markdown(c, m, streaming && m.Status != "completed")
		return
	}
	isReasoning := strings.EqualFold(m.Kind, "reasoning")
	expanded := app.open[m.ID] && !isReasoning
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		title := nativeText(activityTitle(m))
		if s := subagent.Summary(m.Agents); s != "" {
			title += " · " + nativeText(s)
		}
		header := quiet(c.Key("header")).FillWidth().Height(28).PaddingX(0).Label(title)
		header.Children(func() {
			icon(c, activityIcon(m), 14, muted)
			color := muted
			if m.Status == "failed" && !isReasoning {
				color = failed
			}
			if m.Status == "inProgress" || m.Status == "in_progress" || subagent.AnyActive(m.Agents) {
				ui.Spinner(c).FontSize(12)
			}
			ui.Text(c, title).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).TextColor(color)
			if !isReasoning {
				app.chevron(c, expanded)
			}
		})
		if header.Clicked() && !isReasoning {
			app.open[m.ID] = !expanded
		}
		if expanded {
			ui.Column(c).FillWidth().MinWidth(0).Padding(0, 0, 0, 20).Children(func() {
				app.activityDetail(c, m)
			})
		}
	})
}

func (app *conversationView) markdown(c *ui.Context, m Message, streaming bool) {
	content := model.ImagePlaceholder.ReplaceAllString(m.Content, "")
	ui.Column(c).FillWidth().MinWidth(0).Children(func() {
		if streaming {
			markdown.Stream(c, content, &app.md)
		} else {
			markdown.View(c, content, &app.md)
		}
	})
}

func (app *conversationView) activityDetail(c *ui.Context, m Message) {
	if len(m.Plan) > 0 {
		ui.Column(c).Gap(8).Padding(12).Background(subtle).Radius(8).Children(func() {
			for i, p := range m.Plan {
				marker, color := "○", muted
				if p.Status == "completed" {
					marker, color = "✓", success
				} else if p.Status == "in_progress" || p.Status == "inProgress" {
					marker, color = "◉", ink
				}
				ui.Row(c.Key(i)).Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, marker).TextColor(color)
					ui.Text(c, p.Step).Grow(1).MinWidth(0).TextColor(color).FontSize(12)
				})
			}
		})
		return
	}
	if len(m.Agents) > 0 {
		ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
			subAgentList(c, m.Agents)
			// An adapter leaves the content empty when the list says it all.
			if strings.TrimSpace(m.Content) != "" {
				app.activityBody(c, m)
			}
		})
		return
	}
	app.activityBody(c, m)
}

func (app *conversationView) activityBody(c *ui.Context, m Message) {
	kind := strings.ToLower(m.Kind)
	switch {
	case kind == "reasoning" || kind == "plan":
		markdown.View(c, m.Content, &app.md)
	case kind == "filechange" && m.Item != nil:
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for i, f := range m.Item.Changes {
				ui.Column(c.Key(i)).Gap(6).Children(func() {
					ui.Text(c, f.Path).FontSize(12).Selectable()
					diffView(c, f.Diff)
				})
			}
		})
	case kind == "diff":
		diffView(c, m.Content)
	default:
		ui.Column(c).FillWidth().MinWidth(0).Background(codeBg).Radius(8).Padding(12).Children(func() {
			ui.Text(c, nativeText(m.Content)).Font(monoFont).FontSize(12).LineHeight(1.5).TextColor(codeInk).Selectable()
		})
	}
}

func (app *conversationView) listPadding() float32 {
	if app.mobile {
		return 16
	}
	return 28
}

func (app *conversationView) threadInput(c *ui.Context, state *inlineThreadInput, raw, owner, label, placeholder string, size, width float32, minLines, maxLines int, changed func(string)) ui.Element {
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
			if run.Attributes.Tag == composerCommandTag {
				fresh := state.style.commandAttachment(a.Name)
				a.Draw, a.Width, a.Height = fresh.Draw, fresh.Width, fresh.Height
				continue
			}
			ref, ok := model.ParseThreadURL(a.URL)
			if ok {
				fresh := state.style.attachment(ref, a.Name)
				a.Draw, a.Width, a.Height = fresh.Draw, fresh.Width, fresh.Height
			}
		}
	}
	services := c.Services()
	paste := func() {
		// File managers and image sources may also offer plain text. Read
		// attachment formats first, before falling back to that text.
		clipboard := services.ReadClipboardData(transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text)
		if !app.mobile && app.pasteAttachments != nil && app.pasteAttachments(clipboard) {
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
		selection := state.doc.Text().Slice(min(a, z), max(a, z))
		mentions := model.ThreadMentions(inlineThreadRaw(selection))
		if menu.Item(tr("Copy session link")).Disabled(len(mentions) != 1).Chosen() && len(mentions) == 1 {
			services.WriteClipboard(mentions[0].Ref.URL())
		}
		if menu.Item(tr("Copy")).Disabled(a == z).Chosen() {
			copySelection(false)
		}
		if menu.Item(tr("Cut")).Disabled(a == z).Chosen() {
			copySelection(true)
		}
		if menu.Item(tr("Paste")).Chosen() {
			paste()
		}
		if menu.Item(tr("Paste as conversation reference")).Chosen() {
			if data, err := services.ReadClipboardData(transfer.Text).Read(transfer.Text); err == nil {
				if ref, ok := model.ParseThreadURL(strings.TrimSpace(string(data))); ok {
					state.insert(model.ReferenceText(ref, app.referenceTitle(ref)))
				} else {
					if app.setNotice != nil {
						app.setNotice(tr("No valid session link in the clipboard"))
					}
				}
			}
		}
		if menu.Item(tr("Select all")).Chosen() {
			state.doc.SetSelection(0, state.doc.Text().Len())
		}
	}
	in := richtext.EditorBase(c, state.doc, richtext.EditorOptions{
		Label: label, Placeholder: placeholder, NoMarkdown: true, Font: state.style.font,
		OnPaste: func() bool { paste(); return true }, ContextMenu: contextMenu,
		OnCopy:   func(cut bool) bool { copySelection(cut); return true },
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

func (app *conversationView) threadReferenceContent(text string) (string, []threadTag) {
	own, _ := model.SplitReferenceContext(text)
	seen := map[ThreadRef]bool{}
	var tags []threadTag
	for _, m := range model.ThreadMentions(own) {
		if seen[m.Ref] {
			continue
		}
		seen[m.Ref] = true
		title := strings.TrimSpace(m.Title)
		if title == "" {
			title = app.referenceTitle(m.Ref)
		}
		tags = append(tags, threadTag{Ref: m.Ref, Title: title})
	}
	return own, tags
}

func (app *conversationView) threadMessage(c *ui.Context, message Message) {
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
