package nsui

import (
	"encoding/json"
	"fmt"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/automation"
)

func (a *automationsState) attention(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		items := automation.NeedsAttention(a.list.Get())
		if len(items) == 0 {
			return core.Group(nil)
		}
		children := []core.Node{autoHeading(tr("Needs your attention"))}
		for _, item := range items {
			row := []core.Node{autoButton("attention-open-"+item.ID, item.Name, func() {
				a.route.Set(automationRoute{"detail", item.ID})
			}), autoMuted(cx, automation.AttentionLabel(item.Kind)), autoText(item.Detail)}
			if !item.Run.IsZero() {
				row = append(row, autoButton("attention-handled-"+item.ID, "Mark handled", func() {
					a.mutate("automationReview", automation.Request{ID: item.ID, Started: item.Run, Action: "handled"}, nil)
				}))
			}
			children = append(children, autoCard(cx, row...))
		}
		return autoColumn(children...)
	})
}

func (a *automationsState) reviewControls(cx *core.Scope, v automation.View) core.Node {
	var nodes []core.Node
	call := func(id, action, scope string) {
		a.mutate("automationReview", automation.Request{Review: id, Action: action, Scope: scope}, nil)
	}
	for _, r := range v.Reviews {
		actions := []core.Node{
			autoButton("review-deny-"+r.ID, "Deny", func() { call(r.ID, "deny", "") }),
			autoButton("review-once-"+r.ID, "Approve once", func() { call(r.ID, "approve", automation.ScopeOnce) }),
		}
		if r.Key != "" && r.Key != r.Action {
			actions = append(actions, autoButton("review-kind-"+r.ID, fmt.Sprintf(tr("Approve all %s for 7 days"), r.Key),
				func() { call(r.ID, "approve", automation.ScopeKind) }))
			if r.Project != "" {
				actions = append(actions, autoButton("review-project-"+r.ID, fmt.Sprintf(tr("Approve all %s in this project for 7 days"), r.Key),
					func() { call(r.ID, "approve", automation.ScopeProject) }))
			}
		}
		nodes = append(nodes, autoCard(cx, autoHeading(tr("Waiting for your review")), autoCode(r.Action),
			autoMuted(cx, r.Dir), autoMuted(cx, r.Instructions), autoRow(actions...)))
	}
	for _, g := range v.Grants {
		label := fmt.Sprintf(tr("Allowed %s until %s"), g.Key, automation.FormatTime(g.Expires))
		if g.Scope == automation.ScopeOnce {
			label = fmt.Sprintf(tr("Allowed once until %s: %s"), automation.FormatTime(g.Expires), g.Action)
		} else if g.Scope == automation.ScopeProject {
			label = fmt.Sprintf(tr("Allowed %s in %s until %s"), g.Key, g.Project, automation.FormatTime(g.Expires))
		}
		nodes = append(nodes, autoRow(autoText(label), autoButton("review-revoke-"+g.Review, "Revoke", func() { call(g.Review, "revoke", "") })))
	}
	return autoColumn(nodes...)
}

func (a *automationsState) briefing(cx *core.Scope) core.Node {
	view := core.NewSignal[*automation.BriefingView](cx, nil)
	input := core.NewSignal(cx, "")
	busy := core.NewSignal(cx, false)
	call := func(req automation.Request) {
		busy.Set(true)
		a.call("automationBriefing", req, func(raw json.RawMessage, err error) {
			busy.Set(false)
			if err != nil {
				if req.Action == "set" {
					a.err.Set(err.Error())
				}
				return
			}
			var next automation.BriefingView
			if json.Unmarshal(raw, &next) == nil {
				view.Set(&next)
				input.Set(next.Time)
			}
		})
	}
	cx.Post(func() { call(automation.Request{}) })
	return core.Show{When: func() bool { return view.Get() != nil }, Then: func(cx *core.Scope) core.Node {
		return autoCard(cx, autoHeading(tr("Daily briefing")), autoRow(
			base.Input{TestID: "automation-briefing-time", Value: input.Get, OnChange: input.Set, Placeholder: "09:00", Label: tr("Briefing time")},
			ui.Button{Text: tr("Save"), Disabled: busy.Get, OnPress: func() { call(automation.Request{Action: "set", Data: input.Get()}) }},
			ui.Button{Text: tr("Off"), Disabled: func() bool { return busy.Get() || view.Get().Time == "" }, OnPress: func() { call(automation.Request{Action: "set"}) }},
		))
	}}
}

func automationAccessOptions() []ui.Option[automation.Access] {
	opts := []ui.Option[automation.Access]{}
	for _, value := range []automation.Access{"", automation.AccessRead, automation.AccessEdit, automation.AccessExternal, automation.AccessFull} {
		opts = append(opts, ui.Option[automation.Access]{Value: value, Label: tr(automation.AccessText(value))})
	}
	return opts
}
