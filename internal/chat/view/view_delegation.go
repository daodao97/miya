package view

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	dg "miya/internal/delegation"
	"miya/internal/widgets"
)

// receiptShown is how many settled tasks a batch receipt lists before it
// folds the rest. Tasks that need a look are always listed.
const receiptShown = 3

// delegationCall is the task's one home in the timeline: it follows the task
// from submission to its result. A receipt quotes it rather than repeating it.
func (app *View) delegationCall(c *ui.Context, id int, task model.DelegationCard) {
	title := delegationTitle(task)
	label := tr("Delegated task")
	if task.Agent != "" {
		label = fmt.Sprintf(tr("Delegated to %s"), task.Agent)
	}
	expandable := task.Request != "" || task.Quote != "" || task.Detail != "" || task.Blocked != "" || task.Expired
	expanded := expandable && app.Open[id]
	delegationFrame(c).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(2).Padding(4).Children(func() {
			var header ui.Element
			if expandable {
				header = widgets.Quiet(c.Key("summary")).Role(ui.RoleDisclosure).Expanded(expanded)
			} else {
				header = ui.Row(c.Key("summary"))
			}
			header.Grow(1).MinWidth(0).Height(0).MinHeight(48).Padding(6, 8).Gap(10).AlignItems(ui.Center).Radius(8).Label(label + " · " + title)
			header.Children(func() {
				delegationBadge(c)
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, title).FillWidth().FontSize(13).FontWeight(600).TextColor(widgets.Ink).Wrap().MaxLines(2).Ellipsis("…")
					delegationMeta(c, task.State, label, "")
					// Collapsed, the card still says how the task is going.
					if !expanded {
						if summary := delegationSummary(task); summary != "" {
							ui.Text(c, summary).FillWidth().MinWidth(0).FontSize(12).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
						}
					}
				})
				if expandable {
					app.Chevron(c, expanded)
				}
			})
			if expandable && header.Clicked() {
				app.Open[id] = !expanded
			}
			app.delegationThreadLink(c, task.Thread)
		})
		if expanded {
			delegationDivider(c)
			ui.Column(c).FillWidth().MinWidth(0).Gap(14).Padding(12, 16, 14, 16).Children(func() {
				if strings.TrimSpace(task.Request) != "" {
					delegationSection(c.Key("request"), tr("Task brief"), func() { app.delegationMarkdown(c, task.Request) })
				}
				app.delegationResult(c.Key("result"), task, 0)
			})
		}
	})
}

// delegationReceipt quotes the calls whose tasks finished, one row each.
// Each row points back to its call card, which holds the full result.
func (app *View) delegationReceipt(c *ui.Context, g msgfmt.MessageGroup, tasks []model.DelegationCard) {
	folded := func(i int) bool { return i >= receiptShown && !delegationNeedsLook(tasks[i]) }
	foldable := 0
	for i := range tasks {
		if folded(i) {
			foldable++
		}
	}
	all := app.Open[g.ID]
	delegationFrame(c).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(10, 14).Children(func() {
			widgets.Icon(c, "stack-2", 14, widgets.Muted)
			heading := tr("Delegation results")
			if len(tasks) > 1 {
				heading += " · " + fmt.Sprintf(tr("%d tasks"), len(tasks))
			}
			ui.Text(c, heading).Grow(1).MinWidth(0).FontSize(12).FontWeight(600).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
			if g.ShowTimestamp {
				ui.Text(c, msgfmt.FormatMessageTimestamp(g.Timestamp)).Shrink(0).TextColor(widgets.Muted).FontSize(11)
			}
		})
		for i, task := range tasks {
			if !all && folded(i) {
				continue
			}
			c := c.Key(i)
			ui.Column(c).FillWidth().MinWidth(0).Children(func() {
				delegationDivider(c)
				app.delegationReceiptItem(c, task)
			})
		}
		if foldable > 0 {
			label := tr("Show fewer tasks")
			if !all {
				label = fmt.Sprintf(tr("%d more tasks"), foldable)
			}
			delegationDivider(c.Key("more-line"))
			more := widgets.Quiet(c.Key("more")).FillWidth().Height(36).Justify(ui.Center).Radius(0).Label(label)
			more.Children(func() { ui.Text(c, label).FontSize(12).TextColor(widgets.Muted) })
			if more.Clicked() {
				app.Open[g.ID] = !all
			}
		}
	})
}

func (app *View) delegationReceiptItem(c *ui.Context, task model.DelegationCard) {
	ui.Row(c).FillWidth().MinWidth(0).Gap(12).Padding(10, 6, 12, 14).Children(func() {
		ui.Box(c).Width(3).Shrink(0).AlignSelf(ui.Stretch).Background(delegationAccent(task)).Radius(2)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			app.delegationReceiptTitle(c, task)
			read := ""
			if task.Read {
				read = tr("Read by the main agent")
			}
			delegationMeta(c, task.State, task.Agent, read)
			ui.Column(c).FillWidth().MinWidth(0).Padding(2, 0, 0, 0).Children(func() {
				app.delegationResult(c, task, 3)
			})
		})
		app.delegationThreadLink(c, task.Thread)
	})
}

// delegationReceiptTitle shows the call card the receipt quotes when there is one to find.
func (app *View) delegationReceiptTitle(c *ui.Context, task model.DelegationCard) {
	title := delegationTitle(task)
	text := func() {
		ui.Text(c, title).Grow(1).MinWidth(0).FontSize(13).FontWeight(600).TextColor(widgets.Ink).Wrap().MaxLines(2).Ellipsis("…")
	}
	if task.TaskID == "" {
		ui.Row(c).FillWidth().MinWidth(0).Children(text)
		return
	}
	reveal := ui.ButtonBase(c.Key("reveal")).Row().FillWidth().MinWidth(0).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label(tr("Show delegated call") + " · " + title).Tooltip(tr("Show delegated call"))
	if app.Mobile {
		reveal.MinHeight(32)
	}
	reveal.Children(text)
	if reveal.Clicked() {
		app.jumpTask = task.TaskID
		c.After(0)
	}
}

func delegationFrame(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().MinWidth(0).Border(1, widgets.Line).Radius(12).Background(widgets.Canvas)
}

func delegationDivider(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).Shrink(0).Background(widgets.Line)
}

func delegationBadge(c *ui.Context) {
	ui.Row(c).Size(30, 30).Shrink(0).Radius(8).Background(widgets.Bubble).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		widgets.Icon(c, "stack-2", 15, widgets.Muted)
	})
}

// delegationMeta is the line under a title: who, anything else, then the state.
func delegationMeta(c *ui.Context, state string, parts ...string) {
	ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(6).Children(func() {
		dot := func() { ui.Text(c, "·").Shrink(0).FontSize(11).TextColor(widgets.Muted) }
		first := true
		for _, p := range parts {
			if p == "" {
				continue
			}
			if !first {
				dot()
			}
			first = false
			ui.Text(c, p).MinWidth(0).FontSize(11).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
		}
		if label := delegationStatusLabel(state); label != "" {
			if !first {
				dot()
			}
			delegationStatus(c, state, label)
		}
	})
}

func delegationSection(c *ui.Context, label string, body func()) {
	ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(widgets.Muted)
		body()
	})
}

func delegationAccent(task model.DelegationCard) ui.Color {
	if delegationNeedsLook(task) {
		return widgets.WarnInk.Alpha(.8)
	}
	if dg.State(task.State) == dg.Completed {
		return widgets.Success.Alpha(.7)
	}
	return widgets.Muted.Alpha(.4)
}

// revealDelegation scrolls to and opens the call card a receipt pointed at.
func (app *View) revealDelegation(state *ui.ListState, groups []msgfmt.MessageGroup) {
	id := app.jumpTask
	if id == "" {
		return
	}
	app.jumpTask = ""
	for i, g := range groups {
		for _, m := range g.Messages {
			if len(m.Delegations) > 0 && m.Delegations[0].TaskID == id && msgfmt.IsDelegationCall(m) {
				app.Open[m.ID] = true
				state.ScrollTo(i, ui.Start)
				return
			}
		}
	}
}

func delegationNeedsLook(task model.DelegationCard) bool {
	switch dg.State(task.State) {
	case dg.Failed, dg.Interrupted, dg.NeedsAttention:
		return true
	}
	return task.Blocked != ""
}

func delegationTitle(task model.DelegationCard) string {
	if title := strings.Join(strings.Fields(task.Title), " "); title != "" {
		return title
	}
	return tr("Delegated task")
}

// delegationSummary is the one line a collapsed call shows: live progress
// while the task runs, then the opening of its result or failure.
func delegationSummary(task model.DelegationCard) string {
	if task.Expired {
		return tr("The saved result has expired")
	}
	for _, text := range []string{task.Quote, task.Detail} {
		for _, line := range strings.Split(plainMarkdown(text), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return ""
}

// delegationStatusLabel is empty for a state the timeline does not show.
func delegationStatusLabel(state string) string {
	switch dg.State(state) {
	case dg.Queued:
		return tr("Queued")
	case dg.Starting:
		return tr("Starting")
	case dg.NeedsAttention:
		return subagent.StatusLabel(subagent.Unconfirmed)
	case dg.Running, dg.Completed, dg.Failed, dg.Interrupted:
		return subagent.StatusLabel(state)
	}
	return ""
}

func delegationStatus(c *ui.Context, state, label string) {
	marker, color := subAgentMarker(state)
	ui.Row(c).Shrink(0).Gap(4).AlignItems(ui.Center).Children(func() {
		if dg.State(state) == dg.Running {
			ui.Spinner(c).FontSize(11)
		} else {
			ui.Text(c, marker).FontSize(11).TextColor(color)
		}
		ui.Text(c, label).FontSize(11).TextColor(widgets.Muted).SingleLine()
	})
}

// delegationResult shows the result, failure and blocked actions; maxLines
// clamps each of them in a receipt and 0 shows them whole, in sections, the
// result as Markdown.
func (app *View) delegationResult(c *ui.Context, task model.DelegationCard, maxLines int) {
	detail := task.Detail
	if task.Expired {
		detail = tr("The saved result has expired")
	}
	if maxLines > 0 {
		ui.Column(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
			delegationText(c.Key("quote"), plainMarkdown(task.Quote), widgets.Ink, maxLines)
			delegationText(c.Key("detail"), detail, widgets.Muted, maxLines)
			if task.Blocked != "" {
				ui.Text(c, tr("Blocked actions")).FontSize(11).TextColor(widgets.WarnInk)
				delegationText(c.Key("blocked"), task.Blocked, widgets.Muted, maxLines)
			}
		})
		return
	}
	ui.Column(c).FillWidth().MinWidth(0).Gap(14).Children(func() {
		if strings.TrimSpace(task.Quote) != "" {
			delegationSection(c.Key("quote"), tr("Result"), func() { app.delegationMarkdown(c, task.Quote) })
		}
		delegationText(c.Key("detail"), detail, widgets.Muted, 0)
		if task.Blocked != "" {
			ui.Column(c.Key("blocked")).FillWidth().MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, tr("Blocked actions")).FontSize(11).FontWeight(600).TextColor(widgets.WarnInk)
				delegationText(c, task.Blocked, widgets.Muted, 0)
			})
		}
	})
}

// delegationMarkdown renders the agents' Markdown a size smaller than the
// conversation, to sit inside a card.
func (app *View) delegationMarkdown(c *ui.Context, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	box := ui.Column(c).FillWidth().MinWidth(0)
	type local struct {
		options markdown.Options
		base    *markdown.Theme
		window  *ui.Theme
	}
	l := ui.Local(box, "delegation-markdown", func() local { return local{} })
	if l.options.Theme == nil || l.base != app.Md.Theme || l.window != c.Theme() {
		base := app.Md.Theme
		if base == nil {
			base = markdown.DefaultTheme(c.Theme())
		}
		theme := *base
		theme.FontSize = 13
		// Headings inside a card stay under the card's own title.
		for i := range theme.Headings {
			h := &theme.Headings[i]
			h.Size, h.Weight, h.Rule = 1, 600, false
			h.Top, h.Bottom = 0.9, 0.3
		}
		l.options.Theme, l.base, l.window = &theme, app.Md.Theme, c.Theme()
	}
	l.options.OnLink, l.options.LinkMenu, l.options.LoadImage = app.Md.OnLink, app.Md.LinkMenu, app.Md.LoadImage
	box.Children(func() { markdown.View(c, msgfmt.NativeText(text), &l.options) })
}

// plainMarkdown is the text of Markdown without its marks, for the clamped
// lines of a receipt. Headings name the parts rather than say what
// happened, so a preview starts with the text under them.
func plainMarkdown(text string) string {
	text = msgfmt.NativeText(strings.TrimSpace(text))
	var body []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			body = append(body, line)
		}
	}
	if strings.TrimSpace(strings.Join(body, "")) != "" {
		text = strings.Join(body, "\n")
	}
	return strings.TrimSpace(richtext.ParseMarkdown(text).String())
}

func delegationText(c *ui.Context, text string, color ui.Color, maxLines int) {
	if strings.TrimSpace(text) == "" {
		return
	}
	t := ui.Text(c, msgfmt.NativeText(text)).FillWidth().MinWidth(0).Wrap().FontSize(12).LineHeight(1.5).TextColor(color).Selectable()
	if maxLines > 0 {
		t.MaxLines(maxLines).Ellipsis("…")
	}
}

func (app *View) delegationThreadLink(c *ui.Context, raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok || app.Md.OnLink == nil {
		return
	}
	size := float32(32)
	if app.Mobile {
		size = 44
	}
	label := tr("Open target conversation")
	link := widgets.ToolButton(c.Key("thread"), "arrow-right", label).Size(size, size).Shrink(0)
	if app.Md.LinkMenu != nil {
		link.ContextMenu(func(m *ui.Menu) { app.Md.LinkMenu(m, ref.URL()) })
	}
	if link.Clicked() {
		app.Md.OnLink(ref.URL())
	}
}
