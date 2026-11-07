package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"github.com/egoist/mygo/ui/richtext"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	"miya/internal/interaction"
	"miya/internal/textutil"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// conversationHost is the state a conversation renderer reads. It owns no agent or desktop state.
type Host interface {
	ViewThread() model.Thread
	ViewRevision() int
	ViewApprovals() []interaction.Request
	ViewSelectThread(model.Thread)
}

// conversationView owns presentation state shared by desktop and phone.
type View struct {
	C                Host
	Theme            *ui.Theme
	Md               markdown.Options
	Mobile           bool
	ImagePreview     func(string) ui.ImageSource
	ReferenceThread  func(model.ThreadRef) (model.Thread, bool)
	ResolveTitle     func(model.ThreadRef) string
	OnRetry          func(appmodel.RetryRequest)
	OnHandoff        func()
	WorkingAccessory func(*ui.Context)
	DesktopAccessory func(*ui.Context, model.Thread)
	UserGroup        func(*ui.Context, msgfmt.MessageGroup)
	MemoryNotice     func(*ui.Context, *model.MemoryNotice)
	NeedsReadFrame   func(model.Thread, *ui.ListState, int) bool
	PasteAttachments func(transfer.Data) bool
	SetNotice        func(string)
	Lists            map[int]*ui.ListState
	Groups           GroupCache
	Copied, Open     map[int]bool
}

func NewConversationView(host Host) *View {
	return &View{C: host, Theme: widgets.AppTheme(), Lists: map[int]*ui.ListState{}, Copied: map[int]bool{}, Open: map[int]bool{}}
}

func (app *View) ReferenceTitle(ref model.ThreadRef) string {
	if app.ReferenceThread != nil {
		if t, ok := app.ReferenceThread(ref); ok {
			return appmodel.ThreadRefTitle(t)
		}
	}
	if app.ResolveTitle != nil {
		return app.ResolveTitle(ref)
	}
	return tr("Session unavailable")
}

func (app *View) ListFor(id int) *ui.ListState {
	if s := app.Lists[id]; s != nil {
		return s
	}
	s := &ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth}
	app.Lists[id] = s
	return s
}

func (app *View) MessageList(c *ui.Context, t model.Thread) {
	c = c.Key(t.ID)
	groups := app.Groups.ofThread(t, app.C.ViewRevision())
	status, completedGroup := currentTurnStatus(t, groups)
	state := app.ListFor(t.ID)
	state.Key = func(i int) any { return msgfmt.MessageViewKey(groups[i].Messages[0]) }
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
				widgets.MutedText(c, tr("Unable to load conversation"))
				widgets.MutedText(c, t.Status)
				if widgets.Quiet(c.Key("retry")).Children(func() { widgets.MutedText(c, tr("Retry")) }).Clicked() {
					app.C.ViewSelectThread(app.C.ViewThread())
				}
			})
		} else if t.Loading && len(t.Messages) == 0 {
			ui.Row(c).Gap(8).Margin(ui.Auto).Children(func() {
				ui.Spinner(c)
				widgets.MutedText(c, tr("Loading conversation…"))
			})
		} else if len(t.Messages) > 0 {
			widgets.MutedText(c, tr("No visible messages (reasoning-only or unsupported items)")).Margin(ui.Auto)
		}
	})
	messageScrollbars(c, list)
	if app.Mobile {
		// Claim whitespace before the drawer's Draggable ancestor, but keep
		// child buttons as the innermost click targets. A blanket HandleInput
		// would intercept their presses before ButtonBase could handle them.
		_ = list.Clicked()
	}
	// A completed result needs one frame on screen before it is acknowledged.
	// Request that frame even if finishing the turn left the window idle.
	if !app.Mobile && app.NeedsReadFrame != nil && app.NeedsReadFrame(t, state, len(groups)) {
		if w := uiruntime.CurrentWindow(); w != nil && w.IsFocused() && !w.IsMinimized() {
			c.After(0)
		}
	}
	if !state.AtEnd() && len(groups) > 0 {
		ui.Row(c).Justify(ui.Center).Margin(-44, 0, 8, 0).Children(func() {
			jump := widgets.Quiet(c.Key("latest")).Background(widgets.Canvas).Border(1, widgets.Line).Radius(16).Shadow(0, 2, 8, 0, widgets.Shadow)
			jump.Children(func() { widgets.MutedText(c, tr("↓ Latest")) })
			if jump.Clicked() {
				state.ScrollToEnd()
			}
		})
	}
	if status != "" || completedGroup >= 0 {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, app.listPadding(), 6, app.listPadding()).Children(func() {
			row := ui.Row(c.Key("turn-status")).FillWidth().MaxWidth(744).MinHeight(44).Gap(8).AlignItems(ui.Center)
			if app.Mobile {
				row.Gap(6)
			}
			row.Children(func() {
				phoneHint, desktopHint := "", false
				if label := msgfmt.ThreadReconnectLabel(t); t.Busy && label != "" {
					ui.Row(c.Key("reconnecting")).Gap(8).AlignItems(ui.Center).Label(label).Children(func() {
						ui.Spinner(c).FontSize(13).TextColor(widgets.WarnInk)
						ui.Text(c, label).FontSize(12).TextColor(widgets.WarnInk).Tooltip(t.Status)
					})
				} else if status != "" {
					label := widgets.WorkingLabel(c.Key("working"), "clock", status).MinWidth(0).Shrink(1)
					if t.Busy {
						label.Tooltip(tr("Average output speed for this turn, including wait time."))
					} else if completedGroup >= 0 {
						label.Tooltip(msgfmt.FormatMessageTimestamp(groups[completedGroup].Timestamp))
					}
					if hint := appmodel.StalledHint(t, len(app.C.ViewApprovals()) > 0, time.Now()); t.Busy && hint != "" {
						if app.Mobile {
							phoneHint = hint
						} else {
							desktopHint = true
							ui.Text(c, "· "+hint).Grow(1).MinWidth(0).FontSize(12).TextColor(widgets.WarnInk)
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
						ui.Text(c, tr("No response yet")).Shrink(0).SingleLine().FontSize(12).TextColor(widgets.WarnInk).Tooltip(phoneHint)
					}
					if app.WorkingAccessory != nil {
						app.WorkingAccessory(c.Key("accessory"))
					} else if !app.Mobile {
						if app.DesktopAccessory != nil {
							app.DesktopAccessory(c.Key("accessory"), t)
						}
					}
				})
			})
		})
	}
}

func (app *View) TurnStatusVisible(t model.Thread) bool {
	label, completedGroup := currentTurnStatus(t, app.Groups.ofThread(t, app.C.ViewRevision()))
	return label != "" || completedGroup >= 0
}

func (app *View) GroupView(c *ui.Context, g msgfmt.MessageGroup) {
	app.groupViewWithFooter(c, g, true)
}

func (app *View) groupViewWithFooter(c *ui.Context, g msgfmt.MessageGroup, inlineFooter bool) {
	if g.User {
		if app.UserGroup != nil {
			app.UserGroup(c, g)
		} else {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				if g.ShowTimestamp {
					ui.Text(c, msgfmt.FormatMessageTimestamp(g.Timestamp)).AlignSelf(ui.End).TextColor(widgets.Muted).FontSize(11)
				}
				m := g.Messages[0]
				app.MessageView(c.Key(msgfmt.MessageViewKey(m)), m, false)
			})
		}
		return
	}
	group := ui.Column(c).FillWidth().Gap(12)
	hovered := group.Hovered()
	group.Children(func() {
		if g.ShowTimestamp {
			ui.Text(c, msgfmt.FormatMessageTimestamp(g.Timestamp)).AlignSelf(ui.Start).TextColor(widgets.Muted).FontSize(11)
		}
		for _, run := range msgfmt.ActivityRuns(g.Messages) {
			app.activityRunView(c.Key(msgfmt.MessageViewKey(run.Messages[0])), run, g.Streaming)
		}
		if g.Streaming {
			return
		}
		if !inlineFooter {
			return
		}
		size := float32(28)
		if app.Mobile {
			size = 44
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Height(size).Children(func() {
			elapsed := msgfmt.TurnElapsedLabel(g.Messages, false)
			if elapsed != "" {
				ui.Text(c, elapsed).TextColor(widgets.Muted).FontSize(11).Tooltip(msgfmt.FormatMessageTimestamp(g.Timestamp))
			}
			if stats := g.DiffStats; stats.Added > 0 || stats.Deleted > 0 {
				ui.Row(c.Key("diff-stats")).Gap(4).AlignItems(ui.Center).Children(func() {
					if elapsed != "" {
						ui.Text(c, "·").TextColor(widgets.Muted).FontSize(11)
					}
					responseDiffStats(c, stats)
				})
			}
			copied := app.Copied[g.ID]
			if !app.Mobile && !hovered && !copied {
				return
			}
			app.responseActions(c.Key("actions"), g)
		})
	})
}

func (app *View) responseActions(c *ui.Context, g msgfmt.MessageGroup) {
	size, gap := float32(28), float32(8)
	copyAlign, handoffAlign := ui.Center, ui.Center
	if app.Mobile {
		size, gap, copyAlign, handoffAlign = 44, 0, ui.End, ui.Start
	}
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(gap).Children(func() {
		name := "copy"
		if app.Copied[g.ID] {
			name = "check"
		}
		b := widgets.Quiet(c.Key("copy")).Size(size, size).PaddingX(6).Justify(copyAlign).Label(tr("Copy response")).Tooltip(tr("Copy response"))
		b.Children(func() { widgets.Icon(c, name, 15, widgets.Muted) })
		if b.Clicked() {
			c.WriteClipboard(msgfmt.MessageGroupText(g))
			app.Copied[g.ID] = true
		}
		h := widgets.Quiet(c.Key("handoff")).Size(size, size).PaddingX(6).Justify(handoffAlign).Label(tr("Hand off to a new conversation")).Tooltip(tr("Hand off to a new conversation"))
		h.Children(func() { widgets.Icon(c, "arrow-right", 15, widgets.Muted) })
		if h.Clicked() {
			if app.OnHandoff != nil {
				app.OnHandoff()
			}
		}
	})
}

func (app *View) activityRunView(c *ui.Context, run msgfmt.ActivityRun, streaming bool) {
	if !run.Activity {
		app.MessageView(c, run.Messages[0], streaming)
		return
	}
	if len(run.Messages) == 1 {
		app.MessageView(c.Key(msgfmt.MessageViewKey(run.Messages[0])), run.Messages[0], streaming)
		return
	}
	// Keep the current turn visible without saving its automatic expansion as
	// a user preference. Completion and historical replay then collapse it.
	expanded := streaming || app.Open[-run.ID]
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		header := widgets.Quiet(c.Key("header")).PaddingX(0).AlignSelf(ui.Start).Disabled(streaming)
		// Sub-agents inside a collapsed run still show that they work.
		agents := subagent.Latest(run.Messages)
		header.Children(func() {
			widgets.Icon(c, "stack-2", 14, widgets.Muted)
			if subagent.AnyActive(agents) {
				ui.Spinner(c).FontSize(12)
			}
			label := fmt.Sprintf("%d activities", len(run.Messages))
			if s := subagent.Counts(agents); s != "" {
				label += " · " + s
			}
			widgets.MutedText(c, label)
			app.Chevron(c, expanded)
		})
		if header.Clicked() {
			app.Open[-run.ID] = !expanded
		}
		// Collapsed activity details are not built at all.
		if expanded {
			for _, m := range run.Messages {
				app.MessageView(c.Key(msgfmt.MessageViewKey(m)), m, streaming)
			}
		}
	})
}

func (app *View) Chevron(c *ui.Context, open bool) {
	ch := widgets.Icon(c, "chevron-down", 14, widgets.Muted)
	ch.Rotate(ch.Animate("open", map[bool]float32{false: 0, true: 180}[open], 150*time.Millisecond))
}

func (app *View) MessageView(c *ui.Context, m model.Message, streaming bool) {
	presented := msgfmt.Message(m)
	m.Presentation = &presented
	m.Content = presented.Content
	switch m.Role {
	case "memory":
		if app.MemoryNotice != nil {
			app.MemoryNotice(c, m.Memory)
		} else if m.Memory != nil && m.Memory.Explicit {
			ui.Text(c, tr("Miya memory changed. View it on the memory page; edit on your desktop.")).FontSize(12).TextColor(widgets.Muted)
		}
		return
	case "error":
		ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(16).Gap(10).Border(1, widgets.Line).Radius(16).Children(func() {
			widgets.Icon(c, "alert-circle", 16, widgets.WarnInk)
			ui.Text(c, msgfmt.NativeText(tr(m.Content))).Grow(1).MinWidth(0).Selectable()
			if t := app.C.ViewThread(); msgfmt.RetryableError(t, m) {
				size := float32(28)
				if app.Mobile {
					size = 44
				}
				b := widgets.Quiet(c.Key("retry-response")).Size(size, size).Shrink(0).PaddingX(6).Justify(ui.Center).
					Disabled(t.Busy || t.Loading).Label(tr("Retry response")).Tooltip(tr("Retry response"))
				b.Children(func() { widgets.Icon(c, "refresh", 16, widgets.Muted) })
				if b.Clicked() {
					if app.OnRetry != nil {
						app.OnRetry(appmodel.RetryRequest{ThreadID: t.ID, MessageID: m.ID})
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
						loader := widgets.CachedImage
						if app.ImagePreview != nil {
							loader = app.ImagePreview
						}
						if img := loader(src); img != nil {
							ui.Image(c.Key(src), img).Size(80, 80).Fit(ui.Cover).Radius(8).Label(tr("Image preview"))
						} else {
							ui.Box(c.Key(src)).Size(80, 80).Radius(8).Background(widgets.Bubble).Label(tr("Image preview")).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								widgets.Icon(c, "file", 24, widgets.Muted)
							})
						}
					}
				})
			}
			// Short prompts stay compact; long ones wrap within a bounded bubble.
			prompt := ui.Column(c).AlignSelf(ui.End).MaxWidth(560).MinWidth(0).Background(widgets.Bubble).Radius(16).Padding(14).Gap(8)
			if app.Mobile {
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
	expanded := app.Open[m.ID] && !isReasoning
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		title := msgfmt.NativeText(ActivityTitle(m))
		if s := subagent.Summary(m.Agents); s != "" {
			title += " · " + msgfmt.NativeText(s)
		}
		header := widgets.Quiet(c.Key("header")).FillWidth().Height(28).PaddingX(0).Label(title)
		header.Children(func() {
			widgets.Icon(c, msgfmt.Message(m).Icon, 14, widgets.Muted)
			color := widgets.Muted
			if m.Status == "failed" && !isReasoning {
				color = widgets.Failed
			}
			if m.Status == "inProgress" || m.Status == "in_progress" || subagent.AnyActive(m.Agents) {
				ui.Spinner(c).FontSize(12)
			}
			ui.Text(c, title).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).TextColor(color)
			if !isReasoning {
				app.Chevron(c, expanded)
			}
		})
		if header.Clicked() && !isReasoning {
			app.Open[m.ID] = !expanded
		}
		if expanded {
			ui.Column(c).FillWidth().MinWidth(0).Padding(0, 0, 0, 20).Children(func() {
				app.activityDetail(c, m)
			})
		}
	})
}

func (app *View) markdown(c *ui.Context, m model.Message, streaming bool) {
	content := model.ImagePlaceholder.ReplaceAllString(m.Content, "")
	ui.Column(c).FillWidth().MinWidth(0).Children(func() {
		if streaming {
			markdown.Stream(c, content, &app.Md)
		} else {
			markdown.View(c, content, &app.Md)
		}
	})
}

func (app *View) activityDetail(c *ui.Context, m model.Message) {
	presentation := msgfmt.Message(m)
	if len(presentation.Plan) > 0 {
		ui.Column(c).Gap(8).Padding(12).Background(widgets.Subtle).Radius(8).Children(func() {
			for i, p := range presentation.Plan {
				marker, color := "○", widgets.Muted
				if p.Status == "completed" {
					marker, color = "✓", widgets.Success
				} else if p.Status == "in_progress" || p.Status == "inProgress" {
					marker, color = "◉", widgets.Ink
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

func (app *View) activityBody(c *ui.Context, m model.Message) {
	presentation := msgfmt.Message(m)
	switch presentation.Format {
	case "markdown":
		markdown.View(c, presentation.Content, &app.Md)
	case "files":
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for i, f := range presentation.Changes {
				ui.Column(c.Key(i)).Gap(6).Children(func() {
					ui.Text(c, f.Path).FontSize(12).Selectable()
					diffView(c, f.Diff)
				})
			}
		})
	case "diff":
		diffView(c, presentation.Content)
	default:
		ui.Column(c).FillWidth().MinWidth(0).Background(widgets.CodeBg).Radius(8).Padding(12).Children(func() {
			ui.Text(c, msgfmt.NativeText(presentation.Content)).Font(widgets.MonoFont).FontSize(12).LineHeight(1.5).TextColor(widgets.CodeInk).Selectable()
		})
	}
}

func (app *View) listPadding() float32 {
	if app.Mobile {
		return 16
	}
	return 28
}

func (app *View) ThreadInput(c *ui.Context, state *InlineThreadInput, raw, owner, label, placeholder string, size, width float32, minLines, maxLines int, changed func(string)) ui.Element {
	notify := func(text string) { changed(text); c.Invalidate() }
	state.Style, state.Title = MentionStyle(c, size), app.ReferenceTitle
	if state.Owner != owner {
		state.Doc, state.Raw, state.Owner = nil, "", owner
	}
	state.Moved = false
	if state.Doc != nil && state.Raw == raw && InlineThreadRaw(state.Doc.Text()) == raw {
		a, z := state.Doc.Selection()
		state.Moved = a != state.anchor || z != state.caret
	}
	state.Sync(raw, notify)
	// Drawing colors follow the appearance without changing the undo history.
	for _, run := range state.Doc.Text().Runs(0, state.Doc.Text().Len()) {
		if a := run.Attributes.Attachment; a != nil {
			if run.Attributes.Tag == ComposerCommandTag {
				fresh := state.Style.commandAttachment(a.Name)
				a.Draw, a.Width, a.Height = fresh.Draw, fresh.Width, fresh.Height
				continue
			}
			ref, ok := model.ParseThreadURL(a.URL)
			if ok {
				fresh := state.Style.attachment(ref, a.Name)
				a.Draw, a.Width, a.Height = fresh.Draw, fresh.Width, fresh.Height
			}
		}
	}
	services := c.Services()
	paste := func() {
		// File managers and image sources may also offer plain text. Read
		// attachment formats first, before falling back to that text.
		clipboard := services.ReadClipboardData(transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text)
		if !app.Mobile && app.PasteAttachments != nil && app.PasteAttachments(clipboard) {
			services.Invalidate()
			return
		}
		if text, err := clipboard.Read(transfer.Text); err == nil && len(text) > 0 {
			state.insert(string(text))
		}
	}
	copySelection := func(cut bool) {
		a, z := state.Doc.Selection()
		if a == z {
			return
		}
		start, end := min(a, z), max(a, z)
		services.WriteClipboard(InlineThreadRaw(state.Doc.Text().Slice(start, end)))
		if cut {
			state.Doc.Edit(func(e *richtext.Edit) { e.ReplaceString(start, end, "", richtext.Attributes{}); e.Select(start, start) })
		}
	}
	contextMenu := func(menu *ui.Menu) {
		a, z := state.Doc.Selection()
		selection := state.Doc.Text().Slice(min(a, z), max(a, z))
		mentions := model.ThreadMentions(InlineThreadRaw(selection))
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
					state.insert(model.ReferenceText(ref, app.ReferenceTitle(ref)))
				} else {
					if app.SetNotice != nil {
						app.SetNotice(tr("No valid session link in the clipboard"))
					}
				}
			}
		}
		if menu.Item(tr("Select all")).Chosen() {
			state.Doc.SetSelection(0, state.Doc.Text().Len())
		}
	}
	in := richtext.EditorBase(c, state.Doc, richtext.EditorOptions{
		Label: label, Placeholder: placeholder, NoMarkdown: true, Font: state.Style.Font,
		OnPaste: func() bool { paste(); return true }, ContextMenu: contextMenu,
		OnCopy:   func(cut bool) bool { copySelection(cut); return true },
		TagStyle: func(_ string, span *ui.Span) { span.ObjectDescent = size * 0.25 },
	}).FillWidth().MinWidth(0).FocusRing(false).InputOptions(ui.InputOptions{Dismiss: ui.KeyboardDismissInteractive})
	if measured := in.Bounds().W; measured > 0 {
		width = measured
	}
	spans := InlineThreadSpans(state.Doc.Text(), size)
	_, height := ui.ShapeRichText(spans, state.Style.Font, max(1, width)).Size()
	lineHeight := size * 1.6
	height = max(float32(minLines)*lineHeight, min(float32(maxLines)*lineHeight, height))
	in.Height(height)
	if in.Shortcut(ui.Cmd, ui.KeyC) {
		copySelection(false)
	}
	if in.Shortcut(ui.Cmd, ui.KeyX) {
		copySelection(true)
	}

	state.Sync(state.Raw, notify)
	state.anchor, state.caret = state.Doc.Selection()
	return in
}

func (app *View) ThreadReferenceContent(text string) (string, []threadTag) {
	own, _ := model.SplitReferenceContext(text)
	seen := map[model.ThreadRef]bool{}
	var tags []threadTag
	for _, m := range model.ThreadMentions(own) {
		if seen[m.Ref] {
			continue
		}
		seen[m.Ref] = true
		title := strings.TrimSpace(m.Title)
		if title == "" {
			title = app.ReferenceTitle(m.Ref)
		}
		tags = append(tags, threadTag{Ref: m.Ref, Title: title})
	}
	return own, tags
}

func (app *View) threadMessage(c *ui.Context, message model.Message) {
	if message.Role == "user" {
		message.Content = msgfmt.Message(message).Content
	}
	content, tags := app.ThreadReferenceContent(message.Content)
	if len(tags) == 0 {
		message.Content = content
		app.markdown(c, message, false)
		return
	}
	box := ui.Column(c).MinWidth(0)
	options := ui.Local(box, "thread-markdown", func() markdown.Options { return markdown.Options{} })
	options.OnLink, options.LoadImage, options.Theme = app.Md.OnLink, app.Md.LoadImage, app.Md.Theme
	options.LinkMenu = app.Md.LinkMenu
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
		style := MentionStyle(c, c.Theme().FontSize)
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
				base.Font = widgets.MonoFont
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
				if app.Md.LinkMenu != nil {
					link.ContextMenu(func(m *ui.Menu) { app.Md.LinkMenu(m, part.link) })
				}
				if link.Clicked() && app.Md.OnLink != nil {
					app.Md.OnLink(part.link)
				}
			}
		})
		return true
	}
	box.Children(func() { markdown.View(c, strings.TrimRight(content, "\n"), options) })
}
