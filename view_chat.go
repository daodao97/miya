package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// groupCache keeps the presentation groups of the open conversation until
// the controller changes it, so a frame does not regroup its history.
type groupCache struct {
	thread, revision, count int
	busy                    bool
	turn                    string
	groups                  []messageGroup
}

func (g *groupCache) of(c *chatController) []messageGroup {
	t := c.current()
	revision := c.revision()
	if g.groups != nil && g.thread == t.ID && g.revision == revision && g.count == len(t.Messages) && g.busy == t.Busy && g.turn == t.TurnID {
		return g.groups
	}
	*g = groupCache{thread: t.ID, revision: revision, count: len(t.Messages), busy: t.Busy, turn: t.TurnID}
	g.groups = threadMessageGroups(t, groupMessages(t.Messages))
	if g.groups == nil {
		g.groups = []messageGroup{}
	}
	return g.groups
}

func (app *chatApp) listFor(id int) *ui.ListState {
	if s := app.lists[id]; s != nil {
		return s
	}
	s := &ui.ListState{FollowEnd: true, FollowAnimation: &ui.Smooth}
	app.lists[id] = s
	return s
}

func (app *chatApp) chatView(c *ui.Context) {
	cur := app.c.current()
	if cur.ID == 0 {
		app.home(c)
	} else {
		app.messageList(c, cur)
	}
	app.composerArea(c)
}

func (app *chatApp) home(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(100).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Padding(0, 0, 40, 0).Children(func() {
		icon(c, "brand-openai", 46, ink)
		ui.Text(c, tr("Let’s build")).FontSize(28).FontWeight(600)
		pick := quiet(c.Key("pick-project")).Height(36)
		pick.Children(func() {
			ui.Text(c, app.project()).FontSize(26).TextColor(muted)
			icon(c, "chevron-down", 16, muted)
		})
		if pick.Clicked() {
			app.openProjectDialog()
		}
	})
}

// messageList shows the open conversation. Each conversation keeps its own
// list state, so its place and following of the end survive switching.
func (app *chatApp) messageList(c *ui.Context, t Thread) {
	c = c.Key(t.ID)
	groups := app.groups.of(app.c)
	state := app.listFor(t.ID)
	state.Key = func(i int) any { return messageViewKey(groups[i].Messages[0]) }
	if t.Busy {
		// The elapsed time of the running turn counts up.
		c.After(time.Second)
	}
	list := ui.List(c, state, len(groups), func(i int) {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(744).Children(func() {
				app.groupView(c, groups[i])
			})
		})
	}).Grow(1).MinHeight(0).Padding(app.listPadding()).Gap(app.listPadding()).Children(func() {
		if len(t.Messages) == 0 && !t.Loading && t.Status != "" && !t.Busy {
			ui.Column(c).AlignItems(ui.Center).Gap(8).Margin(ui.Auto).Children(func() {
				mutedText(c, tr("Unable to load conversation"))
				mutedText(c, t.Status)
				if quiet(c.Key("retry")).Children(func() { mutedText(c, tr("Retry")) }).Clicked() {
					app.c.selectThread(app.c.current())
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
	if app.mobile {
		// Claim whitespace before the drawer's Draggable ancestor, but keep
		// child buttons as the innermost click targets. A blanket HandleInput
		// would intercept their presses before ButtonBase could handle them.
		_ = list.Clicked()
	}
	if !state.AtEnd() && len(groups) > 0 {
		ui.Row(c).Justify(ui.Center).Margin(-44, 0, 8, 0).Children(func() {
			jump := quiet(c.Key("latest")).Background(canvas).Border(1, line).Radius(16).Shadow(0, 2, 8, 0, shadow)
			jump.Children(func() { mutedText(c, "↓ Latest") })
			if jump.Clicked() {
				state.ScrollToEnd()
			}
		})
	}
	if t.Busy {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, app.listPadding(), 6, app.listPadding()).Children(func() {
			ui.Row(c).FillWidth().MaxWidth(744).Gap(8).AlignItems(ui.Center).Children(func() {
				if label := threadReconnectLabel(t); label != "" {
					ui.Row(c.Key("reconnecting")).Gap(8).AlignItems(ui.Center).Label(label).Children(func() {
						ui.Spinner(c).FontSize(13).TextColor(warnInk)
						ui.Text(c, label).FontSize(12).TextColor(warnInk).Tooltip(t.Status)
					})
				} else {
					workingLabel(c.Key("working"), "clock", threadWorkingLabel(t)).
						Tooltip("本轮平均输出速度，包含等待时间。")
					if hint := stalledHint(t, len(app.c.approvals()) > 0, time.Now()); hint != "" {
						if app.mobile {
							ui.Box(c).Grow(1).MinWidth(0)
							ui.Text(c, "暂未响应").Shrink(0).SingleLine().FontSize(12).TextColor(warnInk).Tooltip(hint)
						} else {
							ui.Text(c, "· "+hint).Grow(1).MinWidth(0).FontSize(12).TextColor(warnInk)
						}
					}
				}
			})
		})
	}
}

func (app *chatApp) groupView(c *ui.Context, g messageGroup) {
	if g.User {
		app.userGroupView(c, g)
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
		size := float32(28)
		if app.mobile {
			size = 44
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Height(size).Children(func() {
			if elapsed := turnElapsedLabel(g.Messages, false); elapsed != "" {
				ui.Text(c, elapsed).TextColor(muted).FontSize(11).Tooltip(formatMessageTimestamp(g.Timestamp))
			}
			if stats := g.DiffStats; stats.Added > 0 || stats.Deleted > 0 {
				ui.Row(c.Key("diff-stats")).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "·").TextColor(muted).FontSize(11)
					ui.Text(c, fmt.Sprintf("+%d", stats.Added)).TextColor(success).FontSize(11)
					ui.Text(c, fmt.Sprintf("−%d", stats.Deleted)).TextColor(danger).FontSize(11)
				})
			}
			copied := app.copied[g.ID]
			if !app.mobile && !hovered && !copied {
				return
			}
			name := "copy"
			if copied {
				name = "check"
			}
			b := quiet(c.Key("copy")).Size(size, size).PaddingX(6).Justify(ui.Center).Label("Copy response").Tooltip("Copy response")
			b.Children(func() { icon(c, name, 15, muted) })
			if b.Clicked() {
				c.WriteClipboard(messageGroupText(g))
				app.copied[g.ID] = true
			}
			h := quiet(c.Key("handoff")).Size(size, size).PaddingX(6).Justify(ui.Center).Label("交付到新对话").Tooltip("交付到新对话")
			h.Children(func() { icon(c, "arrow-right", 15, muted) })
			if h.Clicked() {
				app.handoffToNewThread()
			}
		})
	})
}

func (app *chatApp) activityRunView(c *ui.Context, run activityRun, streaming bool) {
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
		agents := latestSubAgents(run.Messages)
		header.Children(func() {
			icon(c, "stack-2", 14, muted)
			if subAgentsActive(agents) {
				ui.Spinner(c).FontSize(12)
			}
			label := fmt.Sprintf("%d activities", len(run.Messages))
			if s := subAgentCounts(agents); s != "" {
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

func (app *chatApp) chevron(c *ui.Context, open bool) {
	ch := icon(c, "chevron-down", 14, muted)
	ch.Rotate(ch.Animate("open", map[bool]float32{false: 0, true: 180}[open], 150*time.Millisecond))
}

// messageView shows one protocol item; activity is not assistant prose.
func (app *chatApp) messageView(c *ui.Context, m Message, streaming bool) {
	switch m.Role {
	case "memory":
		app.memoryNoticeView(c, m.Memory)
		return
	case "error":
		ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(16).Gap(10).Border(1, line).Radius(16).Children(func() {
			icon(c, "alert-circle", 16, warnInk)
			ui.Text(c, nativeText(tr(m.Content))).Grow(1).MinWidth(0).Selectable()
			if t := app.c.current(); retryableError(t, m) {
				size := float32(28)
				if app.mobile {
					size = 44
				}
				b := quiet(c.Key("retry-response")).Size(size, size).Shrink(0).PaddingX(6).Justify(ui.Center).
					Disabled(t.Busy || t.Loading).Label("重试回复").Tooltip("重试回复")
				b.Children(func() { icon(c, "refresh", 16, muted) })
				if b.Clicked() {
					app.retryResponse(m)
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
							ui.Image(c.Key(src), img).Size(80, 80).Fit(ui.Cover).Radius(8).Label("图片预览")
						} else {
							ui.Box(c.Key(src)).Size(80, 80).Radius(8).Background(bubble).Label("图片预览").AlignItems(ui.Center).Justify(ui.Center).Children(func() {
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
		if s := subAgentSummary(m.Agents); s != "" {
			title += " · " + nativeText(s)
		}
		header := quiet(c.Key("header")).FillWidth().Height(28).PaddingX(0).Label(title)
		header.Children(func() {
			icon(c, activityIcon(m), 14, muted)
			color := muted
			if m.Status == "failed" && !isReasoning {
				color = failed
			}
			if m.Status == "inProgress" || m.Status == "in_progress" || subAgentsActive(m.Agents) {
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

func (app *chatApp) markdown(c *ui.Context, m Message, streaming bool) {
	content := attachmentImagePlaceholder.ReplaceAllString(m.Content, "")
	ui.Column(c).FillWidth().MinWidth(0).Children(func() {
		if streaming {
			markdown.Stream(c, content, &app.md)
		} else {
			markdown.View(c, content, &app.md)
		}
	})
}

func activityTitle(m Message) string {
	active := m.Status == "inProgress" || m.Status == "in_progress"
	switch strings.ToLower(m.Kind) {
	case "claudetool":
		return claudeToolTitle(m)
	case "handoff":
		if m.HandoffTitle != "" {
			return "已交付到对话「" + m.HandoffTitle + "」"
		}
		return "已交付到其他对话"
	case "reasoning":
		if active {
			return tr("Thinking…")
		}
		return tr("Thought")
	case "commandexecution":
		if m.Item != nil && len(m.Item.CommandActions) > 0 {
			a := m.Item.CommandActions[0]
			switch a.Type {
			case "read":
				return "已读取 " + firstNonEmpty(a.Name, a.Path, "文件")
			case "search":
				return "已搜索 " + firstNonEmpty(a.Query, a.Path, "文件")
			case "listFiles":
				return "已列出 " + firstNonEmpty(a.Path, "文件")
			}
		}
		verb := tr("Ran ")
		if active {
			verb = tr("Running ")
		} else if m.Status == "failed" {
			verb = tr("Failed · ")
		} else if m.Status == "interrupted" {
			verb = tr("Stopped · ")
		}
		if m.Item != nil && m.Item.Command != "" {
			return verb + strings.SplitN(m.Item.Command, "\n", 2)[0]
		}
		return verb + "command"
	case "filechange":
		verb := tr("Edited")
		if active {
			verb = tr("Editing")
		} else if m.Status == "failed" {
			verb = tr("Failed to edit")
		}
		if m.Item != nil {
			return fmt.Sprintf("%s %d files", verb, len(m.Item.Changes))
		}
		return verb + " files"
	case "plan":
		if len(m.Plan) > 0 {
			done := 0
			for _, p := range m.Plan {
				if p.Status == "completed" {
					done++
				}
			}
			return fmt.Sprintf("Plan · %d / %d completed", done, len(m.Plan))
		}
		return tr("Proposed plan")
	case "mcptoolcall", "dynamictoolcall":
		if m.Item != nil {
			name := strings.Trim(m.Item.Server+" · "+m.Item.Tool, " ·")
			if name != "" {
				if active {
					return "Calling " + name
				}
				return "Called " + name
			}
		}
	case "websearch":
		if m.Item != nil {
			if active {
				return "Searching · " + m.Item.Query
			}
			return "Searched · " + m.Item.Query
		}
	case "diff":
		return tr("Changes in this turn")
	}
	return messageLabel(m.Kind)
}

func (app *chatApp) activityDetail(c *ui.Context, m Message) {
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

// activityBody is an activity's own detail: its plan text, diff or output.
func (app *chatApp) activityBody(c *ui.Context, m Message) {
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

func diffView(c *ui.Context, diff string) {
	ui.Column(c).FillWidth().MinWidth(0).Background(codeBg).Radius(8).Padding(10).Selectable().Children(func() {
		for i, l := range strings.Split(bounded(diff), "\n") {
			color := muted
			switch {
			case strings.HasPrefix(l, "+"):
				color = added
			case strings.HasPrefix(l, "-"):
				color = failed
			case strings.HasPrefix(l, "@@"):
				color = hunk
			}
			ui.Text(c.Key(i), l).Font(monoFont).FontSize(12).LineHeight(1.5).TextColor(color)
		}
	})
}

// listPadding spaces the conversation; the phone is narrower.
func (app *chatApp) listPadding() float32 {
	if app.mobile {
		return 16
	}
	return 28
}
