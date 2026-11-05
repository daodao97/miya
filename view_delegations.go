package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	dg "miya/internal/delegation"
	"miya/internal/textutil"
)

type collaborationPanelView struct {
	palette             taskPalette
	capability          *collaborationStatus
	from                string
	readOnly, connected bool
	// trailing aligns the toggle to the right edge instead of filling the row.
	trailing bool
	// toggleElsewhere omits the toggle because the caller draws it with
	// drawToggle, as both surfaces do in the turn status row.
	toggleElsewhere bool
	compactToggle   bool
	call            collaborationCall
	openThread      func(string)
}

// The desktop and phone render this same task card and action state machine.
func (p *collaborationPanel) draw(c *ui.Context, v collaborationPanelView) {
	if v.capability == nil || v.capability.Version < 1 {
		return
	}
	if v.connected && p.from != "" {
		p.refresh(v.capability, v.call, false)
	}
	if p.hidden() {
		if v.connected {
			c.After(time.Second)
		}
		return
	}
	if !v.toggleElsewhere {
		ui.Row(c.Key("collaboration")).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			if v.trailing {
				ui.Box(c).Grow(1).MinWidth(0)
			}
			p.drawToggle(c, v)
		})
	}
	if v.connected {
		c.After(time.Second)
	}
}

func (p *collaborationPanel) drawContent(c *ui.Context, v collaborationPanelView) {
	pals := v.palette
	locked := !v.connected || v.readOnly || v.capability.Stale || p.storageError != "" || p.pending != nil
	button := func(c *ui.Context, id, label string, disabled bool, action func()) {
		b := ui.ButtonBase(c.Key(id)).MinHeight(36).Padding(6, 9).Radius(8).Label(label).Disabled(disabled)
		b.Children(func() { ui.Text(c, label).FontSize(12).TextColor(pals.ink) })
		if b.Clicked() {
			action()
		}
	}
	ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
		if !v.connected {
			ui.Text(c, tr("Offline · Showing saved task data")).FontSize(12).TextColor(pals.muted)
		}
		if p.storageError != "" || v.capability.StorageError != "" {
			ui.Text(c, tr("Task storage unavailable: ")+textutil.FirstNonEmpty(p.storageError, v.capability.StorageError)).FontSize(12).TextColor(danger).Wrap().FillWidth()
		}
		if p.paused {
			ui.Row(c).FillWidth().Wrap().Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Receiving results is paused")).FontSize(12).TextColor(pals.muted)
				button(c, "resume", tr("Resume receiving results"), locked, func() { p.submit("resume", nil, v.call) })
			})
		}
		if p.err != "" {
			ui.Text(c, p.err).FontSize(12).TextColor(danger).FillWidth().Wrap().Selectable()
		}
		if p.pending != nil {
			if p.busy {
				ui.Text(c, tr("Submitting task request…")).FontSize(12).TextColor(pals.muted)
			} else {
				button(c, "retry-request", tr("Retry the same request"), !v.connected, func() { p.retry(v.call) })
			}
		}
		if p.selected != "" {
			p.drawDetail(c, v, locked, button)
			return
		}
		if p.loaded && len(p.rows) == 0 {
			ui.Text(c, tr("No collaboration tasks yet")).FontSize(12).TextColor(pals.muted)
		}
		ui.Scroll(c.Key("task-cards")).FillWidth().MaxHeight(218).Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
				for _, task := range p.rows {
					b := ui.ButtonBase(c.Key(task.ID)).FillWidth().MinWidth(0).Padding(9).Radius(9).Border(1, pals.border).Label(task.Title)
					b.Children(func() {
						ui.Column(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, nativeText(task.Title)).FontSize(13).FontWeight(600).TextColor(pals.ink).SingleLine().Ellipsis("…").FillWidth()
							ui.Text(c, backendFor(task.Agent).Name()+" · "+collaborationStateLabel(task.State)).FontSize(11).TextColor(pals.muted)
							if text := textutil.FirstNonEmpty(task.ResultSummary, task.Progress); text != "" {
								ui.Text(c, nativeText(text)).FontSize(12).TextColor(pals.muted).FillWidth().SingleLine().Ellipsis("…")
							}
							if notice := collaborationNotificationLabel(task.Notification); notice != "" {
								ui.Text(c, notice).FontSize(11).TextColor(pals.muted)
							}
						})
					})
					if b.Clicked() {
						p.selected = task.ID
						if v.connected {
							p.read(task.ID, v.call)
						}
					}
				}
			})
		})
		ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
			if p.next != "" {
				button(c, "more", tr("Load more"), p.loading || !v.connected, func() { p.refresh(v.capability, v.call, true) })
			}
			active := false
			for _, task := range p.rows {
				if !dg.Terminal(task.State) {
					active = true
					break
				}
			}
			if active {
				button(c, "cancel-all", tr("Stop all delegated tasks"), locked, func() { p.submit("cancel_all", nil, v.call) })
			}
			button(c, "refresh", tr("Refresh"), !v.connected || p.loading, func() { p.loaded = false; p.loadedAt = time.Time{}; p.refresh(v.capability, v.call, false) })
		})
	})
}

// hidden reports whether the panel has nothing to show. Agents delegate
// tasks, so the panel appears once the conversation has one; a request in
// flight keeps it visible.
func (p *collaborationPanel) hidden() bool {
	return len(p.rows) == 0 && p.pending == nil
}

// drawToggle is the Agent tasks button that opens and closes the panel.
func (p *collaborationPanel) drawToggle(c *ui.Context, v collaborationPanelView) {
	pals := v.palette
	b := ui.ButtonBase(c.Key("toggle")).MinHeight(36).Padding(4, 8).Radius(8).Label(p.title()).MinWidth(0)
	if !v.trailing {
		b.Grow(1)
	}
	b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			icon(c, "git-branch", 14, pals.muted)
			text := p.title()
			if v.compactToggle {
				text = fmt.Sprintf("%d", len(p.rows))
			}
			label := ui.Text(c, text).FontSize(12).SingleLine().Ellipsis("…")
			if v.toggleElsewhere {
				label.FontWeight(400).TextColor(pals.muted)
			} else {
				label.TextColor(pals.ink)
			}
			if p.loading {
				ui.Spinner(c).FontSize(12)
			}
		})
	})
	if b.Clicked() {
		p.open = !p.open
	}
	ui.PopoverBase(c.Key("task-popover"), b, &p.open, func(panel ui.Element) {
		// Bound the whole panel, including task actions, to the space above
		// the status row. Long lists and details scroll inside the overlay.
		safe, root := c.SafeArea(), c.Root().Bounds()
		width := min(float32(560), root.W-safe.Left-safe.Right-24)
		height := min(float32(480), b.Bounds().Y-safe.Top-20)
		panel.AttachTo(b, ui.AnchorTopRight, ui.AnchorBottomRight).Margin(0, 0, 8, 0).
			Width(width).Padding(10).Background(pals.surface).Border(1, pals.border).Radius(12).
			Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.16)).Label(tr("Agent tasks"))
		ui.Scroll(c.Key("task-popover-scroll")).FillWidth().MaxHeight(max(float32(1), height-22)).Children(func() {
			p.drawContent(c, v)
		})
	})
}

type collaborationButton func(*ui.Context, string, string, bool, func())

func (p *collaborationPanel) drawDetail(c *ui.Context, v collaborationPanelView, locked bool, button collaborationButton) {
	pals := v.palette
	button(c, "back", tr("Back to tasks"), false, func() { p.selected = "" })
	task, ok := p.details[p.selected]
	if !ok {
		if p.loadingDetail {
			ui.Text(c, tr("Loading task…")).FontSize(12).TextColor(pals.muted)
		}
		return
	}
	ui.Text(c, nativeText(task.Title)).FontSize(13).FontWeight(600).TextColor(pals.ink).SingleLine().Ellipsis("…").FillWidth()
	ui.Text(c, backendFor(task.Agent).Name()+" · "+collaborationStateLabel(task.State)).FontSize(12).TextColor(pals.muted)
	ui.Text(c, tr("Shared workspace: ")+task.Workspace).FontSize(11).TextColor(pals.muted).SingleLine().Ellipsis("…").FillWidth().Tooltip(task.Workspace)
	if task.Resolution == "abandoned" {
		ui.Text(c, tr("Tracking stopped · Native execution is still unverified")).FontSize(12).TextColor(pals.muted).FillWidth().Wrap()
	}
	if task.RecheckError != "" {
		ui.Text(c, task.RecheckError).FontSize(12).TextColor(danger).Wrap().FillWidth()
	}
	if notice := collaborationNotificationLabel(task.Notification); notice != "" {
		ui.Text(c, notice).FontSize(12).TextColor(pals.muted)
	}
	if task.DeliveryReason != "" {
		ui.Text(c, task.DeliveryReason).FontSize(11).TextColor(pals.muted).Wrap().FillWidth()
	}
	ui.Scroll(c.Key("result")).FillWidth().MaxHeight(180).Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
			if task.Result.Availability == "expired" {
				ui.Text(c, tr("The saved result has expired")).FontSize(12).TextColor(pals.muted)
			}
			text := task.Result.Text
			if text == "" && !dg.Terminal(task.State) {
				text = task.Progress
			}
			if text != "" {
				ui.Text(c, nativeText(text)).FontSize(12).TextColor(pals.ink).LineHeight(1.5).FillWidth().Wrap().Selectable()
			}
			if task.Result.Error != "" {
				ui.Text(c, task.Result.Error).FontSize(12).TextColor(danger).Wrap().FillWidth().Selectable()
			}
			if task.Result.Truncated {
				ui.Text(c, tr("Result snapshot truncated · Open the target conversation for the full output")).FontSize(11).TextColor(pals.muted).Wrap().FillWidth()
			}
			for _, file := range task.Result.Artifacts {
				ui.Text(c, file).FontSize(11).TextColor(pals.muted).FillWidth().Wrap().Selectable()
			}
		})
	})
	ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
		if task.Thread != "" && task.Thread != p.from {
			button(c, "open-target", tr("Open target conversation"), false, func() { p.open = false; v.openThread(task.Thread) })
		}
		if task.From != "" && task.From != p.from {
			button(c, "open-source", tr("Open source conversation"), false, func() { p.open = false; v.openThread(task.From) })
		}
		if !dg.Terminal(task.State) {
			button(c, "cancel", tr("Cancel task"), locked || task.CancelRequested, func() { p.submit("cancel_task", map[string]any{"task_id": task.ID}, v.call) })
		}
		if task.Notification == "watching" || task.Notification == "pending" {
			button(c, "unwatch", tr("Keep result only"), locked, func() { p.submit("unwatch_task", map[string]any{"task_id": task.ID}, v.call) })
		} else if !dg.Terminal(task.State) && task.Resolution != "abandoned" {
			button(c, "watch", tr("Return results automatically"), locked, func() { p.submit("watch_task", map[string]any{"task_id": task.ID}, v.call) })
		}
		if task.State == dg.NeedsAttention {
			button(c, "recheck", tr("Recheck execution"), locked, func() { p.submit("resolve_task", map[string]any{"task_id": task.ID, "action": "recheck"}, v.call) })
			if task.Resolution != "abandoned" {
				button(c, "abandon", tr("Stop tracking"), locked, func() { p.abandon = task.ID })
			}
		}
		if task.Notification == "needsAttention" && task.DeliveryID != "" && slices.Contains(v.capability.Operations, "resolve_delivery") {
			button(c, "resolve-delivery", tr("Stop tracking this notification"), locked, func() {
				p.abandon = task.DeliveryID
			})
		}
	})
	if task.DeliveryID != "" && p.abandon == task.DeliveryID {
		ui.Text(c, tr("The original notification may already have started. Stop tracking it to allow later results; it will not be sent again.")).FontSize(12).TextColor(pals.muted).FillWidth().Wrap()
		button(c, "confirm-delivery-resolution", tr("Allow later results"), locked, func() {
			p.submit("resolve_delivery", map[string]any{"task_id": task.ID, "delivery_id": task.DeliveryID, "reason": "User stopped tracking this uncertain notification to allow later results"}, v.call)
		})
	}
	if p.abandon == task.ID {
		ui.TextAreaBase(c.Key("reason"), &p.reason).Label(tr("Reason for stopping tracking")).Placeholder(tr("Record why this execution cannot be verified")).Lines(2, 3).FillWidth().FontSize(13).Padding(8).Border(1, pals.border).Radius(8).Disabled(locked)
		button(c, "confirm-abandon", tr("Stop tracking this execution"), locked || strings.TrimSpace(p.reason) == "", func() {
			p.submit("resolve_task", map[string]any{"task_id": task.ID, "action": "abandon", "reason": p.reason}, v.call)
		})
	}
}
