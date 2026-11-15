package automation

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"miya/internal/notification"
)

// A run that finds nothing new since the run before is no progress. Runs
// started by triggers that bring no data of their own back off after a
// few of those in a row, and an automation with StallAfter stops its
// triggered runs once that many runs in a row found nothing new, until a
// person edits it or runs it now. docs/automations.md has the rules.

const (
	// backoffAfter is how many triggered runs in a row must find nothing
	// new before the next wake without data waits longer.
	backoffAfter = 2
	// backoffBase is the least wait once backing off; it doubles with each
	// further run that finds nothing new, up to backoffMax.
	backoffBase = 10 * time.Minute
	backoffMax  = 24 * time.Hour
	// maxStallAfter bounds StallAfter by the runs that are kept.
	maxStallAfter = 10
)

// sameReport reports whether two results say the same: the same findings,
// metrics and artifacts, and the same need of the person. The summary is
// left out, as agents word it differently each time.
func sameReport(a, b Report) bool {
	set := func(items []string) []string {
		out := make([]string, 0, len(items))
		for _, s := range items {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	metrics := func(ms []Metric) []string {
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.Text())
		}
		return set(out)
	}
	return a.NeedsUser == b.NeedsUser && slices.Equal(set(a.Findings), set(b.Findings)) &&
		slices.Equal(metrics(a.Metrics), metrics(b.Metrics)) && slices.Equal(set(a.Artifacts), set(b.Artifacts))
}

// previousReport is the result of the latest finished run that started
// before started and has one.
func (s Store) previousReport(id string, started time.Time) *Report {
	runs := s.Runs[id]
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.Started.Before(started) && r.Report != nil && r.Result != Running && r.Result != Skipped {
			return r.Report
		}
	}
	return nil
}

// foundNothingNew reports whether a finished run found nothing new: it
// passed, the agent reported a result itself that needs no one, and the
// run before reported the same. A result Miya made from the last reply is
// not compared, nor is a run without one before it.
func foundNothingNew(run Run, prev *Report) bool {
	r := run.Report
	return run.Result == Passed && r != nil && !r.Auto && !r.NeedsUser && prev != nil && !prev.Auto && sameReport(*r, *prev)
}

// unchangedStreak counts the latest finished runs in a row that found
// nothing new: of those started by triggers when triggeredOnly, of all
// otherwise. Skipped runs are not counted and do not break it.
func (s Store) unchangedStreak(id string, triggeredOnly bool) int {
	runs, n := s.Runs[id], 0
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.Result == Running || r.Result == Skipped || triggeredOnly && !triggered(r.WakeSource) {
			continue
		}
		if !r.Unchanged {
			break
		}
		n++
	}
	return n
}

// backoffUntil is when a wake without data may start a triggered run of a
// again after triggered runs in a row found nothing new, and how many did;
// zero when it need not wait.
func (rn *Runner) backoffUntil(a Automation) (time.Time, int) {
	n := rn.Store.unchangedStreak(a.ID, true)
	if n < backoffAfter {
		return time.Time{}, n
	}
	last := rn.lastTriggeredRun(a.ID)
	if last.IsZero() {
		return time.Time{}, n
	}
	wait := max(time.Duration(a.Policy.CooldownMinutes)*time.Minute, backoffBase)
	for i := backoffAfter; i < n && wait < backoffMax; i++ {
		wait *= 2
	}
	return last.Add(min(wait, backoffMax)), n
}

// afterProgress keeps an automation's stall to what its runs found: a run
// that found something ends it, and StallAfter runs in a row that found
// nothing start it, with a notice.
func (rn *Runner) afterProgress(a Automation, run Run) {
	if run.Result == Running || run.Result == Skipped {
		return
	}
	if !run.Unchanged {
		rn.unstall(a.ID)
		return
	}
	if a.StallAfter <= 0 {
		return
	}
	if _, stalled := rn.Store.Stalled[a.ID]; stalled {
		return
	}
	n := rn.Store.unchangedStreak(a.ID, false)
	if n < a.StallAfter {
		return
	}
	if rn.Store.Stalled == nil {
		rn.Store.Stalled = map[string]time.Time{}
	}
	rn.Store.Stalled[a.ID] = rn.Now()
	rn.Persist()
	rn.host.Changed()
	rn.host.Deliver(notification.Notice{ID: "miya-auto-stall-" + a.ID, Title: a.Name + " · " + tr("Stalled"),
		Body: fmt.Sprintf(tr("%d runs in a row found nothing new."), n) + " " + tr("Triggered runs wait until you edit it or run it now.")})
}

// unstall ends an automation's stall.
func (rn *Runner) unstall(id string) {
	if _, ok := rn.Store.Stalled[id]; ok {
		delete(rn.Store.Stalled, id)
		rn.Persist()
		rn.host.Changed()
	}
}
