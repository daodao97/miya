package account

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"miya/locales"
)

type Caller interface {
	Call(context.Context, string, any, any) error
}

func NewResetAttempt(data Display) (*ResetAttempt, error) {
	credit := data.Limits.ResetCredits.Next()
	if data.Identity == nil || data.Identity.Type != "chatgpt" || credit == nil {
		return nil, errors.New(tr("No reset card details available. Refresh and retry."))
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, errors.New(tr("Unable to create a reset request. Try again."))
	}
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	key := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	return &ResetAttempt{Account: *data.Identity, AccountID: data.Limits.AccountID, Credit: *credit, IdempotencyKey: key}, nil
}

type ResetExecution struct {
	Display     Display
	HaveDisplay bool
	Submitted   bool
	Outcome     string
}

func ResetExpiryText(credit ResetCredit) string {
	if credit.ExpiresAt == nil {
		return tr("This reset card has no expiration.")
	}
	return tr("Next expiration: ") + time.Unix(*credit.ExpiresAt, 0).Local().Format("2006-01-02 15:04")
}

func ResetOutcomeText(outcome string) string {
	switch outcome {
	case "reset":
		return tr("Used 1 reset card. Quota windows reset.")
	case "nothingToReset":
		return tr("No quota windows need resetting. Card retained.")
	case "noCredit":
		return tr("No reset cards available for this account.")
	case "alreadyRedeemed":
		return tr("This reset request already completed. No additional card used.")
	default:
		return ""
	}
}

type ReadResult struct {
	Account *Identity `json:"account"`
}

func QuotaLabel(w *QuotaWindow, index int) string {
	if w.WindowDurationMins != nil {
		m := *w.WindowDurationMins
		if m == 300 {
			return tr("5-hour quota")
		}
		if m == 10080 {
			return tr("Weekly quota")
		}
		if m > 0 && m%1440 == 0 {
			return fmt.Sprintf(tr("%d-day quota"), m/1440)
		}
		if m > 0 && m%60 == 0 {
			return fmt.Sprintf(tr("%d-hour quota"), m/60)
		}
		if m > 0 {
			return fmt.Sprintf(tr("%d-minute quota"), m)
		}
	}
	if index == 0 {
		return tr("Primary quota")
	}
	return tr("Secondary quota")
}

func tr(s string) string { return locales.T(s) }
