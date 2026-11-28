package nsrequests

import (
	"slices"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/nswidgets"
)

type CollaborationHost interface {
	CollaborationCapabilities() *model.CollaborationStatus
	ReadOnly() bool
	OpenReference(string)
}

func settingsColumn(n ...core.Node) core.Node {
	return core.View{Style: core.Style{Gap: 10, MinWidth: core.Pt(0)}, Children: n}
}
func settingsActions(n ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8}, Children: n}
}
func collaborationTaskRow(cx *core.Scope, id string, get func() CollaborationTaskView, press func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	state := func() string {
		t := get()
		return model.AgentName(t.Agent) + " · " + collaborationStateLabel(t.State)
	}
	subtitle := func() string {
		t := get()
		return firstSettingsText(t.ResultSummary, t.Progress)
	}
	if cx.Env().Desktop() {
		return TouchButton(id, func() string {
			return get().Title + " · " + state() + "\n" + subtitle() + "\n" + collaborationNotificationLabel(get().Notification)
		}, nil, press)
	}
	return core.View{TestID: id, Role: core.RoleButton, LabelFunc: func() string { return get().Title + " · " + state() },
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.All(14), MinHeight: core.Pt(52), CornerRadius: 14},
		StyleFunc: func() core.Style {
			return core.Style{Background: colors.Panel(), Active: &core.Style{Background: colors.Hover()}}
		},
		OnPress: func(core.PressEvent) { press() },
		Children: []core.Node{
			nswidgets.Icon("stack-2", 19, colors.Link),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 4}, Children: []core.Node{
				core.Text{ContentFunc: func() string { return get().Title }, Style: core.Style{FontSize: 16, FontWeight: 600}},
				nswidgets.MutedText(cx, state),
				core.Show{When: func() bool { return subtitle() != "" }, Then: func(cx *core.Scope) core.Node {
					return core.Text{ContentFunc: subtitle, MaxLines: 2, EllipsizeMode: core.EllipsizeTail,
						StyleFunc: func() core.Style { return core.Style{FontSize: 13, Color: colors.Muted()} }}
				}},
			}},
			nswidgets.Icon("chevron-right", 14, colors.Muted),
		}}
}

func firstSettingsText(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Plain state-machine fields become bindings through read. The keyed task
// list survives polling; only selection and available actions rebuild regions.
func CollaborationContent(cx *core.Scope, c CollaborationHost, p *CollaborationPanel, call CollaborationCall, bump func(), read func()) core.Node {
	locked := func() bool {
		read()
		cap := c.CollaborationCapabilities()
		return c.ReadOnly() || cap == nil || cap.Stale || p.storageError != "" || cap.StorageError != "" || p.Pending != nil
	}
	button := func(id, title string, disabled func() bool, act func()) core.Node {
		return TouchButton("collaboration-"+id, func() string { return title }, disabled, func() { act(); bump() })
	}
	selected := core.NewMemo(cx, func() string { read(); return p.Selected })
	list := core.Show{When: func() bool { return selected.Get() == "" }, Then: func(cx *core.Scope) core.Node {
		return settingsColumn(
			core.Show{When: func() bool { read(); return len(p.Rows) == 0 && p.Err == "" }, Then: func(cx *core.Scope) core.Node {
				colors := nswidgets.ColorsOf(cx)
				loading := func() bool { read(); return !p.Loaded }
				return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.All(14), MinHeight: core.Pt(52), CornerRadius: 14},
					StyleFunc: func() core.Style { return core.Style{Background: colors.Panel()} },
					Children: []core.Node{
						core.Show{When: loading, Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 18} },
							Else: func(*core.Scope) core.Node { return nswidgets.Icon("stack-2", 18, colors.Muted) }},
						nswidgets.MutedText(cx, func() string {
							if loading() {
								return tr("Loading…")
							}
							return tr("No collaboration tasks yet")
						}),
					}}
			}},
			core.Each(func() []CollaborationTaskView { read(); return p.Rows }, func(t CollaborationTaskView) string { return t.ID }, func(cx *core.Scope, get func() CollaborationTaskView, _ func() int) core.Node {
				id := core.Untracked(cx, func() string { return get().ID })
				return collaborationTaskRow(cx, "collaboration-task-"+id, get, func() { p.Read(id, call); bump() })
			}),
			core.Show{When: func() bool { read(); return p.next != "" }, Then: func(cx *core.Scope) core.Node {
				return button("more", tr("Load more"), func() bool { read(); return p.loading }, func() { p.Refresh(c.CollaborationCapabilities(), call, true) })
			}},
			core.Show{When: func() bool {
				read()
				return slices.ContainsFunc(p.Rows, func(t CollaborationTaskView) bool { return !dg.Terminal(t.State) })
			}, Then: func(cx *core.Scope) core.Node {
				return button("cancel-all", tr("Stop all delegated tasks"), locked, func() { p.Submit("cancel_all", nil, call) })
			}},
			button("refresh", tr("Refresh"), func() bool { read(); return p.loading }, func() {
				p.Loaded = false
				p.LoadedAt = time.Time{}
				p.Refresh(c.CollaborationCapabilities(), call, false)
			}),
		)
	}}
	detail := core.Show{When: func() bool { return selected.Get() != "" }, Then: func(cx *core.Scope) core.Node {
		get := func() CollaborationTaskView { read(); return p.Details[selected.Get()] }
		actionsKey := core.NewMemo(cx, func() string {
			t := get()
			return strings.Join([]string{t.ID, string(t.State), t.Resolution, t.Notification, t.DeliveryID, t.Thread, t.From, p.abandon}, "\x00")
		})
		return settingsColumn(button("back", tr("Back to tasks"), nil, func() { p.Selected = "" }), core.Text{ContentFunc: func() string {
			t := get()
			if t.ID == "" {
				return tr("Loading task…")
			}
			content := []string{t.Title, model.AgentName(t.Agent) + " · " + collaborationStateLabel(t.State), tr("Shared workspace: ") + t.Workspace, t.RecheckError, collaborationNotificationLabel(t.Notification), t.DeliveryReason, firstSettingsText(t.Result.Text, t.Progress), t.Result.Error}
			if t.Resolution == dg.ResolutionAbandoned {
				content = append(content, tr("Tracking stopped · Native execution is still unverified"))
			}
			if t.Result.Availability == dg.ResultExpired {
				content = append(content, tr("The saved result has expired"))
			}
			if t.Result.Truncated {
				content = append(content, tr("Result snapshot truncated · Open the target conversation for the full output"))
			}
			content = append(content, t.Result.Artifacts...)
			return strings.Join(content, "\n")
		}, Selectable: core.SelectableOn, Style: core.Style{FontSize: 12}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				actionsKey.Get()
				return core.Func(func(cx *core.Scope) core.Node {
					t := get()
					if t.ID == "" {
						return core.Group(nil)
					}
					args := func() map[string]any { return map[string]any{"task_id": t.ID} }
					submit := func(op string, a map[string]any) { p.Submit(op, a, call) }
					var actions []core.Node
					for _, link := range []struct{ id, title, ref string }{{"target", "Open target conversation", t.Thread}, {"source", "Open source conversation", t.From}} {
						if link.ref != "" && link.ref != p.From {
							actions = append(actions, button("open-"+link.id, tr(link.title), nil, func() { c.OpenReference(link.ref) }))
						}
					}
					if !dg.Terminal(t.State) {
						actions = append(actions, button("cancel", tr("Cancel task"), func() bool { return locked() || get().CancelRequested }, func() { submit("cancel_task", args()) }))
					}
					if t.Notification == "watching" || t.Notification == "pending" {
						actions = append(actions, button("unwatch", tr("Keep result only"), locked, func() { submit("unwatch_task", args()) }))
					} else if !dg.Terminal(t.State) && t.Resolution != dg.ResolutionAbandoned {
						actions = append(actions, button("watch", tr("Return results automatically"), locked, func() { submit("watch_task", args()) }))
					}
					if t.State == dg.NeedsAttention {
						actions = append(actions, button("recheck", tr("Recheck execution"), locked, func() { a := args(); a["action"] = "recheck"; submit("resolve_task", a) }))
						if t.Resolution != dg.ResolutionAbandoned {
							actions = append(actions, button("abandon", tr("Stop tracking"), locked, func() { p.abandon = t.ID }))
						}
					}
					cap := c.CollaborationCapabilities()
					if t.Notification == "needsAttention" && t.DeliveryID != "" && cap != nil && slices.Contains(cap.Operations, "resolve_delivery") {
						actions = append(actions, button("resolve-delivery", tr("Stop tracking this notification"), locked, func() { p.abandon = t.DeliveryID }))
					}
					nodes := []core.Node{settingsActions(actions...)}
					if t.DeliveryID != "" && p.abandon == t.DeliveryID {
						nodes = append(nodes, core.Text{Content: tr("The original notification may already have started. Stop tracking it to allow later results; it will not be sent again.")}, button("confirm-delivery", tr("Allow later results"), locked, func() {
							submit("resolve_delivery", map[string]any{"task_id": t.ID, "delivery_id": t.DeliveryID, "reason": "User stopped tracking this uncertain notification to allow later results"})
						}))
					}
					if p.abandon == t.ID {
						reason := core.NewSignal(cx, p.reason)
						nodes = append(nodes, ui.Input{Label: tr("Reason for stopping tracking"), Placeholder: tr("Record why this execution cannot be verified"), Value: reason.Get, OnChange: func(v string) { p.reason = v; reason.Set(v) }, Disabled: locked}, button("confirm-abandon", tr("Stop tracking this execution"), func() bool { return locked() || strings.TrimSpace(reason.Get()) == "" }, func() { a := args(); a["action"] = "abandon"; a["reason"] = reason.Get(); submit("resolve_task", a) }))
					}
					return settingsColumn(nodes...)
				})
			}),
		)
	}}
	storageError := func() string {
		read()
		if cap := c.CollaborationCapabilities(); cap != nil {
			return firstSettingsText(p.storageError, cap.StorageError)
		}
		return ""
	}
	return core.Scroll{Style: core.Style{MaxHeight: core.Pt(420)}, Children: []core.Node{settingsColumn(
		core.Show{When: func() bool { return storageError() != "" }, Then: func(*core.Scope) core.Node {
			return core.Text{ContentFunc: func() string { return tr("Task storage unavailable: ") + storageError() }}
		}},
		core.Show{When: func() bool { read(); return p.paused }, Then: func(cx *core.Scope) core.Node {
			return settingsColumn(core.Text{Content: tr("Receiving results is paused")}, button("resume", tr("Resume receiving results"), locked, func() { p.Submit("resume", nil, call) }))
		}},
		core.Show{When: func() bool { read(); return p.Err != "" }, Then: func(*core.Scope) core.Node {
			return core.Text{ContentFunc: func() string { read(); return p.Err }, Selectable: core.SelectableOn}
		}},
		core.Show{When: func() bool { read(); return p.Pending != nil }, Then: func(cx *core.Scope) core.Node {
			return button("retry", tr("Retry the same request"), func() bool { read(); return p.Busy }, func() { p.Retry(call) })
		}}, list, detail,
	)}}
}
