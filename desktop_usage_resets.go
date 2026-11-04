package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/agentusage"
)

func usageResetExpiryText(r *agentusage.ResetCredits) (string, string) {
	if r == nil || r.Count == 0 {
		return tr("No reset cards available"), ""
	}
	if len(r.Credits) == 0 {
		return tr("Expiration unavailable"), ""
	}
	var details []string
	for i, card := range r.Credits {
		when := tr("No expiration")
		if card.ExpiresAt != nil {
			when = time.Unix(*card.ExpiresAt, 0).Local().Format("2006-01-02 15:04") + tr(" expires")
		}
		details = append(details, fmt.Sprintf(tr("Card %d: %s"), i+1, when))
	}
	if len(r.Credits) != r.Count {
		return fmt.Sprintf(tr("Expiration available for %d / %d cards"), len(r.Credits), r.Count), strings.Join(details, "\n")
	}
	text := tr("No expiration")
	if at := r.Credits[0].ExpiresAt; at != nil {
		text = tr("Next expiration ") + time.Unix(*at, 0).Local().Format("01-02 15:04")
	}
	return text, strings.Join(details, "\n")
}

func (a *chatApp) usageResetSection(c *ui.Context, r agentusage.Reading, compact bool) {
	d := a.usage
	if d == nil || r.Resets == nil && d.resetMessage == "" {
		return
	}
	theme := c.Theme()
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Box(c).FillWidth().Height(0.5).Background(theme.Border)
		if r.Resets != nil {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "refresh", 13, theme.TextMuted)
				ui.Text(c, fmt.Sprintf(tr("%d reset cards"), r.Resets.Count)).FontSize(12).FontWeight(500).TextColor(theme.Text).Grow(1)
				label := tr("Use a card…")
				retry := d.resetRetry && d.resetAttempt != nil && d.resetAttempt.AccountKey == r.AccountKey
				if retry {
					label = tr("Retry reset…")
				}
				if d.resetting {
					label = tr("Using card…")
				}
				button := ui.Button(c, label).Label(tr("Use Codex reset")).Disabled(d.busy || r.Error != "" || r.AccountKey == "" || r.Home == "" || r.Resets.Count == 0 && !retry)
				if compact {
					button.FontSize(11).Height(25).Padding(0, 8)
				}
				if button.Clicked() {
					a.beginUsageReset(r)
				}
			})
			text, details := usageResetExpiryText(r.Resets)
			ui.Text(c, text).FontSize(11).TextColor(theme.TextMuted).Tooltip(details)
		}
		if d.resetMessage != "" {
			ui.Text(c, d.resetMessage).FillWidth().FontSize(11).LineHeight(1.4).TextColor(theme.TextMuted)
		}
	})
}

func (a *chatApp) beginUsageReset(r agentusage.Reading) {
	d := a.usage
	if d == nil || d.closed || d.busy {
		return
	}
	if !d.resetRetry || d.resetAttempt == nil || d.resetAttempt.AccountKey != r.AccountKey || d.resetAttempt.Home != r.Home {
		attempt, err := agentusage.NewResetAttempt(r)
		if err != nil {
			d.resetMessage = err.Error()
			a.invalidateUsageViews()
			return
		}
		d.resetAttempt = &attempt
		d.resetRetry = false
	}
	d.resetReading, d.resetOpen = r, true
	a.invalidateUsageViews()
}

func (a *chatApp) usageResetConfirmation(c *ui.Context, _ agentusage.Reading) {
	d := a.usage
	if d == nil || d.resetAttempt == nil {
		return
	}
	r := d.resetReading
	theme := c.Theme()
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		title := tr("Use a Codex reset card?")
		if d.resetRetry {
			title = tr("Retry the last reset?")
		}
		ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(theme.Text)
		if r.Account != "" {
			ui.Text(c, r.Account).FillWidth().FontSize(11).TextColor(theme.TextMuted).SingleLine().Ellipsis("…").Tooltip(r.Account)
		}
		body := tr("Uses 1 card to reset eligible quota cycles. This cannot be undone.")
		if d.resetRetry {
			body = tr("The last result is unconfirmed. Retrying the same operation will not use another card.")
		}
		ui.Text(c, body).FillWidth().FontSize(11).LineHeight(1.5).TextColor(theme.TextMuted)
		selection := tr("Codex will select an available card.")
		if d.resetRetry {
			selection = tr("Only retries the last confirmed operation without selecting another card.")
		} else if d.resetAttempt.CreditID != "" {
			selection = tr("Uses the card that expires soonest.")
			if text, _ := usageResetExpiryText(r.Resets); text != "" {
				selection += "\n" + text
			}
		}
		ui.Text(c, selection).FillWidth().FontSize(11).LineHeight(1.4).TextColor(theme.TextMuted)
		if d.resetMessage != "" && d.resetRetry {
			ui.Text(c, d.resetMessage).FillWidth().FontSize(11).TextColor(theme.TextMuted)
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, tr("Cancel")).Label(tr("Cancel Codex reset")).Disabled(d.resetting).Clicked() {
				d.resetOpen = false
				a.invalidateUsageViews()
			}
			label := tr("Confirm")
			if d.resetRetry {
				label = tr("Retry operation")
			}
			if d.resetting {
				label = tr("Using card…")
			}
			if ui.Button(c, label).Label(tr("Confirm Codex reset")).Disabled(d.busy).Clicked() {
				a.consumeUsageReset()
			}
		})
	})
}

func (a *chatApp) consumeUsageReset() {
	d := a.usage
	if d == nil || d.closed || d.busy || !d.resetOpen || d.resetAttempt == nil {
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.busy, d.resetting = true, true
	attempt := *d.resetAttempt
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	a.invalidateUsageViews()
	var cl *serviceClient
	if a.c.remoteHost() {
		cl = a.c.remote
	}
	go func() {
		defer cancel()
		var result agentusage.ResetResult
		var err error
		if cl == nil {
			result, err = agentusage.UseCodexReset(ctx, attempt)
		} else {
			err = cl.call("host", hostRequest{Action: "usage-reset", Reset: &attempt}, &result)
		}
		mygo.RunOnMain(func() {
			if d.closed {
				return
			}
			d.busy, d.resetting, d.cancel = false, false, nil
			if err != nil {
				d.resetRetry, d.resetMessage = true, err.Error()
			} else {
				d.resetRetry, d.resetOpen, d.resetAttempt = false, false, nil
				d.resetMessage = usageResetOutcome(result.Outcome)
				if result.Reading.Error != "" {
					d.resetMessage += tr("; failed to read quota, please refresh")
				}
				found := false
				for i := range d.readings {
					if d.readings[i].Agent == "codex" {
						d.readings[i], found = result.Reading, true
					}
				}
				if !found {
					d.readings = append(d.readings, result.Reading)
				}
				a.syncUsageTray()
			}
			d.timer = time.AfterFunc(3*time.Minute, func() { mygo.RunOnMain(a.refreshUsage) })
			a.invalidateUsageViews()
		})
	}()
}

func usageResetOutcome(outcome string) string {
	switch outcome {
	case "reset":
		return tr("Used 1 reset card. Quota updated.")
	case "alreadyRedeemed":
		return tr("The last reset completed. No additional card used.")
	case "nothingToReset":
		return tr("No eligible cycles to reset. No card used.")
	case "noCredit":
		return tr("No reset cards left for this account")
	}
	return tr("Refresh to check reset card status")
}
