package mobile

import (
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

// sendPhoneReview approves, denies or revokes on the desktop
// (docs/action-review.md); the reply brings the automations back.
func (m *App) sendPhoneReview(review, action, scope, note string) {
	a := &m.Autos
	if a.ReviewBusy || !m.Connected || m.Conn == nil || !m.State.AutomationReview {
		return
	}
	body, _ := json.Marshal(automation.Request{Review: review, Action: action, Scope: scope, Note: note})
	a.ReviewBusy = true
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(remote.Request{Op: "automation-review", Automation: body}); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn {
					a.ReviewBusy, a.Err = false, tr("Could not send the decision: ")+err.Error()
				}
			})
		}
	}()
}

// ReceiveAutomationReview takes the desktop's reply to a decision.
func (m *App) ReceiveAutomationReview(data json.RawMessage, message string) {
	a := &m.Autos
	a.ReviewBusy = false
	if message != "" {
		a.Err = message
	}
	var list []automation.View
	if json.Unmarshal(data, &list) == nil && list != nil {
		a.List, a.Loaded = list, true
	}
}

// automationPhoneReviews shows an automation's actions waiting for review,
// to approve or deny, and what the person allowed it, to revoke.
func (m *App) automationPhoneReviews(c *ui.Context, v automation.View) {
	p, a := m.Pal, &m.Autos
	if len(v.Reviews) == 0 && len(v.Grants) == 0 {
		return
	}
	can := m.Connected && m.State.AutomationReview && !a.ReviewBusy
	if a.reviewNotes == nil {
		a.reviewNotes = map[string]*string{}
	}
	for _, r := range v.Reviews {
		note, ok := a.reviewNotes[r.ID]
		if !ok {
			note = new(string)
			a.reviewNotes[r.ID] = note
		}
		ui.Column(c.Key("review:"+r.ID)).FillWidth().Padding(14, 16).Gap(10).Radius(18).Background(p.surface).Children(func() {
			ui.Text(c, tr("Waiting for your review")).FontSize(m.Font(15)).FontWeight(600)
			ui.Text(c, r.Action).FontSize(m.Font(14)).Font(widgets.MonoFont).Selectable().Wrap()
			if r.Dir != "" {
				ui.Text(c, fmt.Sprintf(tr("Runs in %s"), r.Dir)).FontSize(m.Font(13)).TextColor(p.muted).Wrap()
			}
			if r.Instructions != "" {
				ui.Text(c, fmt.Sprintf(tr("Its instructions: %s"), textutil.ClipRunes(r.Instructions, 300))).FontSize(m.Font(13)).TextColor(p.muted).Wrap()
			}
			ui.TextInput(c.Key("note"), note).Label(tr("Note to the automation (optional)")).FillWidth().FontSize(m.Font(15))
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if m.pill(c, "deny", tr("Deny"), false).Disabled(!can).Clicked() {
					m.sendPhoneReview(r.ID, "deny", "", *note)
				}
				if m.pill(c, "once", tr("Approve once"), true).Disabled(!can).Clicked() {
					m.sendPhoneReview(r.ID, "approve", automation.ScopeOnce, *note)
				}
			})
			if r.Key != "" && r.Key != r.Action {
				if m.pill(c, "kind", fmt.Sprintf(tr("Approve all %s for 7 days"), r.Key), false).Disabled(!can).Clicked() {
					m.sendPhoneReview(r.ID, "approve", automation.ScopeKind, *note)
				}
				if r.Project != "" && m.pill(c, "project", fmt.Sprintf(tr("Approve all %s in this project for 7 days"), r.Key), false).Disabled(!can).Clicked() {
					m.sendPhoneReview(r.ID, "approve", automation.ScopeProject, *note)
				}
			}
		})
	}
	if len(v.Grants) == 0 {
		return
	}
	ui.Column(c.Key("grants")).FillWidth().Padding(14, 16).Gap(10).Radius(18).Background(p.surface).Children(func() {
		ui.Text(c, tr("Allowed actions")).FontSize(m.Font(13)).TextColor(p.muted)
		for _, g := range v.Grants {
			text := fmt.Sprintf(tr("Allowed %s until %s"), g.Key, automation.FormatTime(g.Expires))
			if g.Scope == automation.ScopeOnce {
				text = fmt.Sprintf(tr("Allowed once until %s: %s"), automation.FormatTime(g.Expires), g.Action)
			}
			if g.Scope == automation.ScopeProject {
				text = fmt.Sprintf(tr("Allowed %s in %s until %s"), g.Key, g.Project, automation.FormatTime(g.Expires))
			}
			ui.Row(c.Key("grant:" + g.Review)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, text).Grow(1).MinWidth(0).FontSize(m.Font(14)).Wrap()
				if m.pill(c, "revoke", tr("Revoke"), false).Disabled(!can).Clicked() {
					m.sendPhoneReview(g.Review, "revoke", "", "")
				}
			})
		}
	})
}
