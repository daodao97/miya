package desktopui

import (
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

// automationReviews lists the actions an automation sent for review, to
// approve or deny, and what the person allowed it, to revoke
// (docs/action-review.md).
func (app *App) automationReviews(c *ui.Context, v automation.View) {
	if len(v.Reviews) == 0 && len(v.Grants) == 0 {
		return
	}
	call := func(review, action, scope string) {
		app.autoCall("automationReview", automation.Request{Review: review, Action: action, Scope: scope}, func(_ json.RawMessage, err error) {
			if err != nil {
				app.Autos.err = err.Error()
			}
			app.loadAutomations()
		})
	}
	for _, r := range v.Reviews {
		ui.Column(c.Key("review:"+r.ID)).FillWidth().Gap(8).Padding(12).Border(1, widgets.Line).Radius(10).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				widgets.Icon(c, "shield-check", 14, autoBad)
				ui.Text(c, tr("Waiting for your review")).FontSize(13).FontWeight(600).Grow(1).MinWidth(0)
				ui.Text(c, automation.FormatTime(r.Requested)).FontSize(12).TextColor(widgets.Muted).Shrink(0)
			})
			ui.Text(c, r.Action).FontSize(13).FillWidth()
			if r.Dir != "" {
				ui.Text(c, fmt.Sprintf(tr("Runs in %s"), r.Dir)).FontSize(12).TextColor(widgets.Muted)
			}
			if r.Instructions != "" {
				ui.Text(c, fmt.Sprintf(tr("Its instructions: %s"), textutil.ClipRunes(r.Instructions, 300))).FontSize(12).TextColor(widgets.Muted).FillWidth()
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "").Grow(1)
				if widgets.Quiet(c.Key("deny")).PaddingX(8).Label(tr("Deny")).Children(func() {
					ui.Text(c, tr("Deny")).FontSize(12)
				}).Clicked() {
					call(r.ID, "deny", "")
				}
				if r.Key != "" && r.Key != r.Action {
					kind := fmt.Sprintf(tr("Approve all %s for 7 days"), r.Key)
					if widgets.Quiet(c.Key("kind")).PaddingX(8).Label(kind).Children(func() {
						ui.Text(c, kind).FontSize(12)
					}).Clicked() {
						call(r.ID, "approve", automation.ScopeKind)
					}
					if r.Project != "" {
						project := fmt.Sprintf(tr("Approve all %s in this project for 7 days"), r.Key)
						if widgets.Quiet(c.Key("project")).PaddingX(8).Label(project).Children(func() {
							ui.Text(c, project).FontSize(12)
						}).Clicked() {
							call(r.ID, "approve", automation.ScopeProject)
						}
					}
				}
				if ui.PrimaryButton(c.Key("once"), tr("Approve once")).Clicked() {
					call(r.ID, "approve", automation.ScopeOnce)
				}
			})
		})
	}
	if len(v.Grants) == 0 {
		return
	}
	ui.Column(c.Key("grants")).FillWidth().Gap(6).Padding(12).Border(1, widgets.Line).Radius(10).Children(func() {
		ui.Text(c, tr("Allowed actions")).FontSize(12).TextColor(widgets.Muted)
		for _, g := range v.Grants {
			text := fmt.Sprintf(tr("Allowed %s until %s"), g.Key, automation.FormatTime(g.Expires))
			if g.Scope == automation.ScopeOnce {
				text = fmt.Sprintf(tr("Allowed once until %s: %s"), automation.FormatTime(g.Expires), g.Action)
			}
			if g.Scope == automation.ScopeProject {
				text = fmt.Sprintf(tr("Allowed %s in %s until %s"), g.Key, g.Project, automation.FormatTime(g.Expires))
			}
			ui.Row(c.Key("grant:" + g.Review)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, text).FontSize(12).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				if widgets.Quiet(c.Key("revoke")).PaddingX(8).Label(tr("Revoke")).Children(func() {
					ui.Text(c, tr("Revoke")).FontSize(12)
				}).Clicked() {
					call(g.Review, "revoke", "")
				}
			})
		}
	})
}
