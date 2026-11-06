package widgets

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/textutil"
)

type CollaborationPanelView struct {
	Palette             TaskPalette
	Capability          *model.CollaborationStatus
	From                string
	ReadOnly, Connected bool
	// trailing aligns the toggle to the right edge instead of filling the row.
	Trailing bool
	// toggleElsewhere omits the toggle because the caller draws it with
	// drawToggle, as both surfaces do in the turn status row.
	ToggleElsewhere bool
	CompactToggle   bool
	Call            collaborationCall
	OpenThread      func(string)
}

// The desktop and phone render this same task card and action state machine.
func (p *CollaborationPanel) Draw(c *ui.Context, v CollaborationPanelView) {
	if v.Capability == nil || v.Capability.Version < 1 {
		return
	}
	if v.Connected && p.From != "" {
		p.Refresh(v.Capability, v.Call, false)
	}
	if p.Hidden() {
		if v.Connected {
			c.After(time.Second)
		}
		return
	}
	if !v.ToggleElsewhere {
		ui.Row(c.Key("collaboration")).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
			if v.Trailing {
				ui.Box(c).Grow(1).MinWidth(0)
			}
			p.DrawToggle(c, v)
		})
	}
	if v.Connected {
		c.After(time.Second)
	}
}

func (p *CollaborationPanel) drawContent(c *ui.Context, v CollaborationPanelView) {
	pals := v.Palette
	locked := !v.Connected || v.ReadOnly || v.Capability.Stale || p.storageError != "" || p.Pending != nil
	button := func(c *ui.Context, id, label string, disabled bool, action func()) {
		b := ui.ButtonBase(c.Key(id)).MinHeight(36).Padding(6, 9).Radius(8).Label(label).Disabled(disabled)
		b.Children(func() { ui.Text(c, label).FontSize(12).TextColor(pals.Ink) })
		if b.Clicked() {
			action()
		}
	}
	ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
		if !v.Connected {
			ui.Text(c, tr("Offline · Showing saved task data")).FontSize(12).TextColor(pals.Muted)
		}
		if p.storageError != "" || v.Capability.StorageError != "" {
			ui.Text(c, tr("Task storage unavailable: ")+textutil.FirstNonEmpty(p.storageError, v.Capability.StorageError)).FontSize(12).TextColor(Danger).Wrap().FillWidth()
		}
		if p.paused {
			ui.Row(c).FillWidth().Wrap().Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Receiving results is paused")).FontSize(12).TextColor(pals.Muted)
				button(c, "resume", tr("Resume receiving results"), locked, func() { p.Submit("resume", nil, v.Call) })
			})
		}
		if p.Err != "" {
			ui.Text(c, p.Err).FontSize(12).TextColor(Danger).FillWidth().Wrap().Selectable()
		}
		if p.Pending != nil {
			if p.Busy {
				ui.Text(c, tr("Submitting task request…")).FontSize(12).TextColor(pals.Muted)
			} else {
				button(c, "retry-request", tr("Retry the same request"), !v.Connected, func() { p.Retry(v.Call) })
			}
		}
		if p.Selected != "" {
			p.drawDetail(c, v, locked, button)
			return
		}
		if p.Loaded && len(p.Rows) == 0 {
			ui.Text(c, tr("No collaboration tasks yet")).FontSize(12).TextColor(pals.Muted)
		}
		ui.Scroll(c.Key("task-cards")).FillWidth().MaxHeight(218).Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
				for _, task := range p.Rows {
					b := ui.ButtonBase(c.Key(task.ID)).FillWidth().MinWidth(0).Padding(9).Radius(9).Border(1, pals.Border).Label(task.Title)
					b.Children(func() {
						ui.Column(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, msgfmt.NativeText(task.Title)).FontSize(13).FontWeight(600).TextColor(pals.Ink).SingleLine().Ellipsis("…").FillWidth()
							ui.Text(c, model.AgentName(task.Agent)+" · "+collaborationStateLabel(task.State)).FontSize(11).TextColor(pals.Muted)
							if text := textutil.FirstNonEmpty(task.ResultSummary, task.Progress); text != "" {
								ui.Text(c, msgfmt.NativeText(text)).FontSize(12).TextColor(pals.Muted).FillWidth().SingleLine().Ellipsis("…")
							}
							if notice := collaborationNotificationLabel(task.Notification); notice != "" {
								ui.Text(c, notice).FontSize(11).TextColor(pals.Muted)
							}
						})
					})
					if b.Clicked() {
						p.Selected = task.ID
						if v.Connected {
							p.Read(task.ID, v.Call)
						}
					}
				}
			})
		})
		ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
			if p.next != "" {
				button(c, "more", tr("Load more"), p.loading || !v.Connected, func() { p.Refresh(v.Capability, v.Call, true) })
			}
			active := false
			for _, task := range p.Rows {
				if !dg.Terminal(task.State) {
					active = true
					break
				}
			}
			if active {
				button(c, "cancel-all", tr("Stop all delegated tasks"), locked, func() { p.Submit("cancel_all", nil, v.Call) })
			}
			button(c, "refresh", tr("Refresh"), !v.Connected || p.loading, func() { p.Loaded = false; p.LoadedAt = time.Time{}; p.Refresh(v.Capability, v.Call, false) })
		})
	})
}

// hidden reports whether the panel has nothing to show. Agents delegate
// tasks, so the panel appears once the conversation has one; a request in
// flight keeps it visible.
func (p *CollaborationPanel) Hidden() bool {
	return len(p.Rows) == 0 && p.Pending == nil
}

// drawToggle is the Agent tasks button that opens and closes the panel.
func (p *CollaborationPanel) DrawToggle(c *ui.Context, v CollaborationPanelView) {
	pals := v.Palette
	b := ui.ButtonBase(c.Key("toggle")).MinHeight(36).Padding(4, 8).Radius(8).Label(p.title()).MinWidth(0)
	if !v.Trailing {
		b.Grow(1)
	}
	b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			Icon(c, "git-branch", 14, pals.Muted)
			text := p.title()
			if v.CompactToggle {
				text = fmt.Sprintf("%d", len(p.Rows))
			}
			label := ui.Text(c, text).FontSize(12).SingleLine().Ellipsis("…")
			if v.ToggleElsewhere {
				label.FontWeight(400).TextColor(pals.Muted)
			} else {
				label.TextColor(pals.Ink)
			}
			if p.loading {
				ui.Spinner(c).FontSize(12)
			}
		})
	})
	if b.Clicked() {
		p.Open = !p.Open
	}
	ui.PopoverBase(c.Key("task-popover"), b, &p.Open, func(panel ui.Element) {
		// Bound the whole panel, including task actions, to the space above
		// the status row. Long lists and details scroll inside the overlay.
		safe, root := c.SafeArea(), c.Root().Bounds()
		width := min(float32(560), root.W-safe.Left-safe.Right-24)
		height := min(float32(480), b.Bounds().Y-safe.Top-20)
		panel.AttachTo(b, ui.AnchorTopRight, ui.AnchorBottomRight).Margin(0, 0, 8, 0).
			Width(width).Padding(10).Background(pals.Surface).Border(1, pals.Border).Radius(12).
			Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.16)).Label(tr("Agent tasks"))
		ui.Scroll(c.Key("task-popover-scroll")).FillWidth().MaxHeight(max(float32(1), height-22)).Children(func() {
			p.drawContent(c, v)
		})
	})
}

type collaborationButton func(*ui.Context, string, string, bool, func())

func (p *CollaborationPanel) drawDetail(c *ui.Context, v CollaborationPanelView, locked bool, button collaborationButton) {
	pals := v.Palette
	button(c, "back", tr("Back to tasks"), false, func() { p.Selected = "" })
	task, ok := p.Details[p.Selected]
	if !ok {
		if p.loadingDetail {
			ui.Text(c, tr("Loading task…")).FontSize(12).TextColor(pals.Muted)
		}
		return
	}
	ui.Text(c, msgfmt.NativeText(task.Title)).FontSize(13).FontWeight(600).TextColor(pals.Ink).SingleLine().Ellipsis("…").FillWidth()
	ui.Text(c, model.AgentName(task.Agent)+" · "+collaborationStateLabel(task.State)).FontSize(12).TextColor(pals.Muted)
	ui.Text(c, tr("Shared workspace: ")+task.Workspace).FontSize(11).TextColor(pals.Muted).SingleLine().Ellipsis("…").FillWidth().Tooltip(task.Workspace)
	if task.Resolution == "abandoned" {
		ui.Text(c, tr("Tracking stopped · Native execution is still unverified")).FontSize(12).TextColor(pals.Muted).FillWidth().Wrap()
	}
	if task.RecheckError != "" {
		ui.Text(c, task.RecheckError).FontSize(12).TextColor(Danger).Wrap().FillWidth()
	}
	if notice := collaborationNotificationLabel(task.Notification); notice != "" {
		ui.Text(c, notice).FontSize(12).TextColor(pals.Muted)
	}
	if task.DeliveryReason != "" {
		ui.Text(c, task.DeliveryReason).FontSize(11).TextColor(pals.Muted).Wrap().FillWidth()
	}
	ui.Scroll(c.Key("result")).FillWidth().MaxHeight(180).Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
			if task.Result.Availability == "expired" {
				ui.Text(c, tr("The saved result has expired")).FontSize(12).TextColor(pals.Muted)
			}
			text := task.Result.Text
			if text == "" && !dg.Terminal(task.State) {
				text = task.Progress
			}
			if text != "" {
				ui.Text(c, msgfmt.NativeText(text)).FontSize(12).TextColor(pals.Ink).LineHeight(1.5).FillWidth().Wrap().Selectable()
			}
			if task.Result.Error != "" {
				ui.Text(c, task.Result.Error).FontSize(12).TextColor(Danger).Wrap().FillWidth().Selectable()
			}
			if task.Result.Truncated {
				ui.Text(c, tr("Result snapshot truncated · Open the target conversation for the full output")).FontSize(11).TextColor(pals.Muted).Wrap().FillWidth()
			}
			for _, file := range task.Result.Artifacts {
				ui.Text(c, file).FontSize(11).TextColor(pals.Muted).FillWidth().Wrap().Selectable()
			}
		})
	})
	ui.Row(c).FillWidth().Wrap().Gap(4).Children(func() {
		if task.Thread != "" && task.Thread != p.From {
			button(c, "open-target", tr("Open target conversation"), false, func() { p.Open = false; v.OpenThread(task.Thread) })
		}
		if task.From != "" && task.From != p.From {
			button(c, "open-source", tr("Open source conversation"), false, func() { p.Open = false; v.OpenThread(task.From) })
		}
		if !dg.Terminal(task.State) {
			button(c, "cancel", tr("Cancel task"), locked || task.CancelRequested, func() { p.Submit("cancel_task", map[string]any{"task_id": task.ID}, v.Call) })
		}
		if task.Notification == "watching" || task.Notification == "pending" {
			button(c, "unwatch", tr("Keep result only"), locked, func() { p.Submit("unwatch_task", map[string]any{"task_id": task.ID}, v.Call) })
		} else if !dg.Terminal(task.State) && task.Resolution != "abandoned" {
			button(c, "watch", tr("Return results automatically"), locked, func() { p.Submit("watch_task", map[string]any{"task_id": task.ID}, v.Call) })
		}
		if task.State == dg.NeedsAttention {
			button(c, "recheck", tr("Recheck execution"), locked, func() { p.Submit("resolve_task", map[string]any{"task_id": task.ID, "action": "recheck"}, v.Call) })
			if task.Resolution != "abandoned" {
				button(c, "abandon", tr("Stop tracking"), locked, func() { p.abandon = task.ID })
			}
		}
		if task.Notification == "needsAttention" && task.DeliveryID != "" && slices.Contains(v.Capability.Operations, "resolve_delivery") {
			button(c, "resolve-delivery", tr("Stop tracking this notification"), locked, func() {
				p.abandon = task.DeliveryID
			})
		}
	})
	if task.DeliveryID != "" && p.abandon == task.DeliveryID {
		ui.Text(c, tr("The original notification may already have started. Stop tracking it to allow later results; it will not be sent again.")).FontSize(12).TextColor(pals.Muted).FillWidth().Wrap()
		button(c, "confirm-delivery-resolution", tr("Allow later results"), locked, func() {
			p.Submit("resolve_delivery", map[string]any{"task_id": task.ID, "delivery_id": task.DeliveryID, "reason": "User stopped tracking this uncertain notification to allow later results"}, v.Call)
		})
	}
	if p.abandon == task.ID {
		ui.TextAreaBase(c.Key("reason"), &p.reason).Label(tr("Reason for stopping tracking")).Placeholder(tr("Record why this execution cannot be verified")).Lines(2, 3).FillWidth().FontSize(13).Padding(8).Border(1, pals.Border).Radius(8).Disabled(locked)
		button(c, "confirm-abandon", tr("Stop tracking this execution"), locked || strings.TrimSpace(p.reason) == "", func() {
			p.Submit("resolve_task", map[string]any{"task_id": task.ID, "action": "abandon", "reason": p.reason}, v.Call)
		})
	}
}
