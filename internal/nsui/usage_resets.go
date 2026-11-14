package nsui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/agent/account"
	"miya/internal/agent/quota"
	svcproto "miya/internal/service/protocol"
)

func usageResetExpiryText(r *account.ResetCreditsSummary) (string, string) {
	if r == nil || r.AvailableCount == 0 {
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
	if len(r.Credits) != r.AvailableCount {
		return fmt.Sprintf(tr("Expiration available for %d / %d cards"), len(r.Credits), r.AvailableCount), strings.Join(details, "\n")
	}
	text := tr("No expiration")
	if at := r.Credits[0].ExpiresAt; at != nil {
		text = tr("Next expiration ") + time.Unix(*at, 0).Local().Format("01-02 15:04")
	}
	return text, strings.Join(details, "\n")
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

func (d *desktopUsage) resetMatches(r quota.Reading) bool {
	return d.reset != nil && d.reset.Agent == r.Agent && d.reset.AccountKey == r.AccountKey && d.reset.Home == r.Home
}
func (d *desktopUsage) resetSection(cx *core.Scope, r quota.Reading) core.Node {
	var nodes []core.Node
	if r.Resets != nil {
		label := tr("Use a card…")
		if d.resetRetry && d.resetMatches(r) {
			label = tr("Retry reset…")
		}
		if d.resetting {
			label = tr("Using card…")
		}
		nodes = append(nodes, settingsActions(settingsText(cx, func() string { return fmt.Sprintf(tr("%d reset cards"), r.Resets.AvailableCount) }), settingsButton("usage-reset-"+r.Agent, label, func() { d.beginReset(r) }, func() bool {
			d.revision.Get()
			return d.busy || r.Error != "" || r.AccountKey == "" || r.Home == "" || r.Resets.AvailableCount == 0 && !(d.resetRetry && d.resetMatches(r))
		})))
		text, details := usageResetExpiryText(r.Resets)
		nodes = append(nodes, settingsText(cx, func() string { return text + "\n" + details }))
	}
	if d.resetMessage != "" {
		nodes = append(nodes, settingsText(cx, func() string { return d.resetMessage }))
	}
	return settingsColumn(nodes...)
}
func (d *desktopUsage) beginReset(r quota.Reading) {
	if !d.active || d.busy {
		return
	}
	if !d.resetRetry || !d.resetMatches(r) {
		attempt, err := quota.NewResetAttempt(r)
		if err != nil {
			d.resetMessage = tr(err.Error())
			d.changed()
			return
		}
		d.reset = &attempt
		d.resetRetry = false
		d.resetMessage = ""
	}
	d.resetReading, d.resetOpen = r, true
	d.changed()
}
func (d *desktopUsage) resetConfirmation(cx *core.Scope) core.Node {
	title, body := tr("Use a Codex reset card?"), tr("Uses 1 card to reset eligible quota cycles. This cannot be undone.")
	selection := tr("Codex will select an available card.")
	label := tr("Confirm")
	if d.resetRetry {
		title, body = tr("Retry the last reset?"), tr("The last result is unconfirmed. Retrying the same operation will not use another card.")
		selection = tr("Only retries the last confirmed operation without selecting another card.")
		label = tr("Retry operation")
	} else if d.reset.CreditID != "" {
		selection = tr("Uses the card that expires soonest.")
		text, _ := usageResetExpiryText(d.resetReading.Resets)
		selection += "\n" + text
	}
	if d.resetting {
		label = tr("Using card…")
	}
	return settingsColumn(settingsText(cx, func() string { return title }), settingsText(cx, func() string { return d.resetReading.Account }), settingsText(cx, func() string { return body }), settingsText(cx, func() string { return selection }), settingsText(cx, func() string { return d.resetMessage }), settingsActions(settingsButton("usage-reset-cancel", tr("Cancel"), func() { d.resetOpen = false; d.changed() }, func() bool { d.revision.Get(); return d.resetting }), settingsButton("usage-reset-confirm", label, d.consumeReset, func() bool { d.revision.Get(); return d.busy })))
}
func (d *desktopUsage) consumeReset() {
	if !d.active || d.busy || !d.resetOpen || d.reset == nil {
		return
	}
	if d.timer != nil {
		d.timer()
		d.timer = nil
	}
	attempt := *d.reset
	gen, cl, remote := d.generation, d.client, d.remote
	service := quotaService()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.busy, d.resetting = true, true
	d.changed()
	go func() {
		defer cancel()
		var result quota.ResetResult
		var err error
		if remote {
			if cl == nil {
				err = svcproto.ErrClosed
			} else {
				err = cl.Call("host", svcproto.HostRequest{Action: "usage-reset", Reset: &attempt}, &result)
			}
		} else {
			result, err = service.Reset(ctx, attempt)
		}
		d.cx.Post(func() {
			if !d.active || gen != d.generation {
				return
			}
			d.busy, d.resetting, d.cancel = false, false, nil
			if err != nil {
				d.resetRetry, d.resetMessage = true, err.Error()
			} else {
				d.resetRetry, d.resetOpen, d.reset = false, false, nil
				d.resetMessage = usageResetOutcome(result.Outcome)
				if result.Reading.Error != "" {
					d.resetMessage += tr("; failed to read quota, please refresh")
				}
				found := false
				for i, r := range d.readings {
					if r.Agent == result.Reading.Agent {
						d.readings[i], found = result.Reading, true
					}
				}
				if !found {
					d.readings = append(d.readings, result.Reading)
				}
			}
			d.changed()
			d.schedule()
		})
	}()
}
