package automation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"miya/internal/notification"
)

// A daily briefing sums the last 24 hours of every automation up in one
// notice at a time the person picks: how many runs passed, failed or found
// nothing new, and what waits for them. Code writes it from the runs; no
// model is asked. A day with nothing to say sends nothing.

// BriefingView is the briefing's setting as the pages show it.
type BriefingView struct {
	// Time is when it comes each day, as 15:04; empty when it is off.
	Time string `json:"time"`
	// Last is when the latest one was due and looked at.
	Last time.Time `json:"last,omitzero"`
}

// briefingItems bounds what waits for the person in one notice.
const briefingItems = 5

// NormalizeBriefing checks a time of day for the briefing and writes it as
// 15:04; empty turns it off.
func NormalizeBriefing(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		if t, err = time.Parse("15", s); err != nil {
			return "", errors.New("Write the time as HH:MM, for example 09:00")
		}
	}
	return t.Format("15:04"), nil
}

func (rn *Runner) BriefingView() BriefingView {
	return BriefingView{Time: rn.Store.Briefing, Last: rn.Store.BriefedAt}
}

// SetBriefing sets when the briefing comes, or turns it off. The first one
// comes at the next such time, not at once for a time already past today.
func (rn *Runner) SetBriefing(at string) (BriefingView, error) {
	at, err := NormalizeBriefing(at)
	if err != nil {
		return rn.BriefingView(), err
	}
	rn.Store.Briefing, rn.Store.BriefedAt = at, rn.Now()
	rn.Changed()
	return rn.BriefingView(), nil
}

// briefingDue is today's time of the briefing when it has come and was not
// looked at since; zero otherwise. One missed while asleep comes on waking.
func (rn *Runner) briefingDue(now time.Time) time.Time {
	t, err := time.Parse("15:04", rn.Store.Briefing)
	if rn.Store.Briefing == "" || err != nil {
		return time.Time{}
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if now.Before(at) || !rn.Store.BriefedAt.Before(at) {
		return time.Time{}
	}
	return at
}

// tickBriefing sends the briefing when it is due.
func (rn *Runner) tickBriefing(now time.Time) {
	at := rn.briefingDue(now)
	if at.IsZero() {
		return
	}
	rn.Store.BriefedAt = now
	rn.Persist()
	if body, ok := rn.BriefingText(now.Add(-24*time.Hour), now); ok {
		rn.host.Deliver(notification.Notice{ID: "miya-briefing-" + at.Format("2006-01-02"), Title: tr("Miya · Daily briefing"), Body: body})
	}
}

// BriefingText sums up the runs that ended after since and what waits for
// the person now; ok is false when there is nothing to say.
func (rn *Runner) BriefingText(since, now time.Time) (string, bool) {
	total, passed, failed, quiet := 0, 0, 0, 0
	views := make([]View, 0, len(rn.Store.Automations))
	for _, a := range rn.Store.Automations {
		views = append(views, rn.View(a, now))
		for _, r := range rn.Store.Runs[a.ID] {
			if r.Result == Running || r.Result == Skipped || !r.Finished.After(since) {
				continue
			}
			total++
			switch r.Result {
			case Passed:
				passed++
				if r.Quiet {
					quiet++
				}
			case Failed:
				failed++
			}
		}
	}
	waiting := NeedsAttention(views)
	if total == 0 && len(waiting) == 0 {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, tr("%d runs in the last 24 hours: %d passed, %d failed."), total, passed, failed)
	if quiet > 0 {
		b.WriteString(" " + fmt.Sprintf(tr("%d found nothing new."), quiet))
	}
	if len(waiting) > 0 {
		b.WriteString("\n" + tr("Needs you:"))
		for i, w := range waiting {
			if i == briefingItems {
				b.WriteString("\n" + fmt.Sprintf(tr("and %d more"), len(waiting)-i))
				break
			}
			b.WriteString("\n- " + w.Name + " · " + AttentionLabel(w.Kind))
		}
	}
	return b.String(), true
}
