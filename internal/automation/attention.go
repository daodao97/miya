package automation

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// What across all automations waits for the person: the pages list it
// above the automations so that it is seen without opening each one.

// Attention is one thing an automation waits for a person about.
type Attention struct {
	ID, Name, Project string
	// Kind is one of the Attention kinds; Detail says what in a line.
	Kind   string
	Detail string
	At     time.Time
}

const (
	AttentionStalled   = "stalled"
	AttentionNeedsUser = "needs_user"
	AttentionMerge     = "merge"
	AttentionFailed    = "failed"
	AttentionTrigger   = "trigger"
	AttentionPartial   = "partial"
	AttentionSilent    = "silent"
)

// attentionRank orders kinds by how soon they need the person.
var attentionRank = map[string]int{AttentionStalled: 0, AttentionNeedsUser: 0, AttentionMerge: 1, AttentionFailed: 1, AttentionPartial: 1, AttentionTrigger: 2, AttentionSilent: 2}

// AttentionLabel says a kind in a few words.
func AttentionLabel(kind string) string {
	switch kind {
	case AttentionStalled:
		return tr("Stalled")
	case AttentionNeedsUser:
		return tr("Needs your attention")
	case AttentionMerge:
		return tr("Changes waiting to merge")
	case AttentionFailed:
		return tr("Last run failed")
	case AttentionTrigger:
		return tr("Trigger check failing")
	case AttentionPartial:
		return tr("Covered only part")
	case AttentionSilent:
		return tr("Trigger has gone quiet")
	}
	return kind
}

// latestFinished is the latest run of v that ended and was not skipped.
func latestFinished(v View) (Run, bool) {
	for i := len(v.Runs) - 1; i >= 0; i-- {
		if r := v.Runs[i]; r.Result != Running && r.Result != Skipped {
			return r, true
		}
	}
	return Run{}, false
}

// NeedsAttention lists what the automations wait for a person about, most
// pressing first and, among those alike, newest first: a stall, a latest
// run that needs the person, failed or left changes to merge, and an
// enabled trigger whose check keeps failing.
func NeedsAttention(views []View) []Attention { return NeedsAttentionAt(views, time.Now()) }

// NeedsAttentionAt is NeedsAttention as of now; a command that keeps
// running and has printed nothing for longer than its SilentHours is
// listed too.
func NeedsAttentionAt(views []View, now time.Time) []Attention {
	var out []Attention
	for _, v := range views {
		add := func(kind, detail string, at time.Time) {
			out = append(out, Attention{ID: v.ID, Name: v.Name, Project: v.Project, Kind: kind, Detail: detail, At: at})
		}
		if !v.Stalled.IsZero() {
			add(AttentionStalled, FormatTime(v.Stalled), v.Stalled)
		}
		if r, ok := latestFinished(v); ok && !v.Running {
			at := r.Finished
			switch {
			case r.Report != nil && r.Report.NeedsUser:
				add(AttentionNeedsUser, r.Summary, at)
			case WorktreePending(r) && !r.Changes.Empty():
				add(AttentionMerge, WorktreeLabel(r), at)
			case r.Result == Failed:
				add(AttentionFailed, r.Summary, at)
			case r.Report != nil && r.Report.Coverage.Partial():
				add(AttentionPartial, r.Report.Coverage.Text(), at)
			}
		}
		if v.Enabled {
			for _, t := range v.Triggers {
				st := v.TriggerStates[t.ID]
				if t.Disabled {
					continue
				}
				text := textOr(v.TriggerTexts[t.ID], DescribeTrigger(t, nil))
				if st.Errors >= probeErrorNotice {
					add(AttentionTrigger, text+": "+st.Error, st.LastProbe)
					continue
				}
				// Silence counts from the last line, or from when the command
				// started when it never printed one.
				if c := t.Condition; c != nil && c.When == WhenLine && c.SilentHours > 0 {
					since := st.LastSuccess
					if since.IsZero() {
						since = st.LastProbe
					}
					if !since.IsZero() && now.Sub(since) > time.Duration(c.SilentHours)*time.Hour {
						add(AttentionSilent, text+": "+fmt.Sprintf(tr("nothing printed since %s"), FormatTime(since)), since)
					}
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Attention) int {
		if d := cmp.Compare(attentionRank[a.Kind], attentionRank[b.Kind]); d != 0 {
			return d
		}
		return b.At.Compare(a.At)
	})
	return out
}
