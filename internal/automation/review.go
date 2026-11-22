package automation

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/approval"
	"miya/internal/notification"
	"miya/internal/textutil"
)

// SourceReview is a wake that brings the person's decision on an action an
// automation sent for review (docs/action-review.md).
const SourceReview = "review"

// A review is in one of these states.
const (
	ReviewPending  = "pending"
	ReviewApproved = "approved"
	ReviewDenied   = "denied"
	ReviewExpired  = "expired"
)

// An approval covers this much.
const (
	// ScopeOnce allows the same action once, within a day.
	ScopeOnce = "once"
	// ScopeKind allows actions of the same kind for a week.
	ScopeKind = "kind"
	// ScopeProject allows actions of the same kind for a week in every
	// conversation in the automation's project, ordinary ones included.
	ScopeProject = "project"
)

const (
	reviewExpiry = 7 * 24 * time.Hour
	onceGrant    = 24 * time.Hour
	kindGrant    = 7 * 24 * time.Hour
	maxReviews   = 200
)

// Review is an action an automation with external access asked for, kept
// until the person decides on it.
type Review struct {
	ID         string    `json:"id"`
	Automation string    `json:"automation"`
	Run        time.Time `json:"run,omitzero"`
	Thread     int       `json:"thread,omitempty"`
	// Action is the command or request in full, Key its kind (ActionKey),
	// Dir where it would run.
	Action string `json:"action"`
	Key    string `json:"key,omitempty"`
	Dir    string `json:"dir,omitempty"`
	// Project is the automation's project, which a ScopeProject approval
	// covers.
	Project string `json:"project,omitempty"`
	// Instructions are the automation's instructions when it asked, to
	// hold the action against.
	Instructions string    `json:"instructions,omitempty"`
	Requested    time.Time `json:"requested"`
	State        string    `json:"state"`
	Decided      time.Time `json:"decided,omitzero"`
	Scope        string    `json:"scope,omitempty"`
	Note         string    `json:"note,omitempty"`
	// Delivered marks a decision a run has been told.
	Delivered bool `json:"delivered,omitempty"`
}

// Grant lets an automation take actions a person approved without asking
// again.
type Grant struct {
	Automation string `json:"automation"`
	Review     string `json:"review"`
	Scope      string `json:"scope"`
	// Action is the one action a ScopeOnce grant allows; Key the kind a
	// ScopeKind or ScopeProject grant allows.
	Action string `json:"action,omitempty"`
	Key    string `json:"key,omitempty"`
	// Project is where a ScopeProject grant allows its kind.
	Project string    `json:"project,omitempty"`
	Expires time.Time `json:"expires"`
}

// review answers an outside action of an external automation: a grant
// allows it; otherwise it goes to the person, and this call is refused.
func (rn *Runner) review(a Automation, r *ActiveRun, dir, action string) Answer {
	now := rn.Now()
	key := approval.ActionKey(action)
	if rn.useGrant(a.ID, a.Project, action, key, now) {
		return Answer{Decided: true, Allow: true}
	}
	id := ""
	for _, v := range rn.Store.Reviews {
		if v.Automation == a.ID && v.State == ReviewPending && v.Action == action {
			id = v.ID
		}
	}
	if id == "" {
		id = NewID()
		rn.Store.Reviews = append(rn.Store.Reviews, Review{ID: id, Automation: a.ID, Run: r.started, Thread: r.Thread, Action: action, Key: key, Dir: dir, Project: a.Project,
			Instructions: a.Instructions, Requested: now, State: ReviewPending})
		rn.trimReviews()
		rn.Persist()
		rn.host.Changed()
		rn.host.Deliver(notification.Notice{ID: "miya-auto-review-" + id, Title: a.Name + " · " + tr("Waiting for your review"), Body: textutil.ClipRunes(action, 300), Urgent: true})
	}
	return Answer{Decided: true, Message: fmt.Sprintf("Miya sent this action to the user for review as #%s; it did not run. Do not try another way to do it. Finish the work that does not depend on it, then end this run, saying in report_result what waits for review. When the user decides, Miya starts a new run of this automation with the decision; if it is approved, run the same command again then.", id)}
}

// useGrant reports whether a grant allows action to automation id, or to a
// conversation in project, using up a one-time one and dropping expired
// ones. Only a ScopeProject grant reaches past its automation.
func (rn *Runner) useGrant(id, project, action, key string, now time.Time) bool {
	kept := rn.Store.Grants[:0]
	allowed := false
	for _, g := range rn.Store.Grants {
		if now.After(g.Expires) {
			continue
		}
		if !allowed && g.Scope == ScopeProject && project != "" && g.Key == key && samePath(g.Project, project) {
			allowed = true
		}
		if !allowed && id != "" && g.Automation == id {
			if g.Scope == ScopeOnce && g.Action == action {
				allowed = true
				continue
			}
			if g.Scope == ScopeKind && g.Key == key {
				allowed = true
			}
		}
		kept = append(kept, g)
	}
	if len(kept) != len(rn.Store.Grants) {
		rn.Store.Grants = kept
		rn.Persist()
		rn.host.Changed()
	}
	return allowed
}

// DecideReview records the person's decision on a pending review and wakes
// its automation with it.
func (rn *Runner) DecideReview(id string, approve bool, scope, note string) error {
	i := rn.findReview(id)
	if i < 0 {
		return fmt.Errorf("Review %s not found", id)
	}
	r := &rn.Store.Reviews[i]
	if r.State != ReviewPending {
		return fmt.Errorf("Review %s was already decided", id)
	}
	now := rn.Now()
	r.State, r.Decided, r.Note = ReviewDenied, now, strings.TrimSpace(note)
	if approve {
		if scope == ScopeProject && r.Project == "" {
			scope = ScopeKind
		}
		if scope != ScopeKind && scope != ScopeProject {
			scope = ScopeOnce
		}
		r.State, r.Scope = ReviewApproved, scope
		g := Grant{Automation: r.Automation, Review: r.ID, Scope: scope, Action: r.Action, Expires: now.Add(onceGrant)}
		if scope != ScopeOnce {
			g = Grant{Automation: r.Automation, Review: r.ID, Scope: scope, Key: r.Key, Expires: now.Add(kindGrant)}
		}
		if scope == ScopeProject {
			g.Project = r.Project
		}
		rn.Store.Grants = append(rn.Store.Grants, g)
	}
	rn.Persist()
	rn.host.Changed()
	rn.wakeForReview(*r)
	return nil
}

// RevokeGrant ends the grant a review made.
func (rn *Runner) RevokeGrant(review string) bool {
	kept := rn.Store.Grants[:0]
	for _, g := range rn.Store.Grants {
		if g.Review != review {
			kept = append(kept, g)
		}
	}
	revoked := len(kept) != len(rn.Store.Grants)
	rn.Store.Grants = kept
	if revoked {
		rn.Persist()
		rn.host.Changed()
	}
	return revoked
}

// expireReviews ends reviews nobody decided on in time, as denied.
func (rn *Runner) expireReviews(now time.Time) {
	for i := range rn.Store.Reviews {
		r := &rn.Store.Reviews[i]
		if r.State == ReviewPending && now.Sub(r.Requested) > reviewExpiry {
			r.State, r.Decided = ReviewExpired, now
			rn.Persist()
			rn.host.Changed()
			rn.wakeForReview(*r)
		}
	}
}

func (rn *Runner) wakeForReview(r Review) {
	if _, ok := rn.Store.Find(r.Automation); !ok {
		return
	}
	_, _ = rn.Wake(WakeEvent{Automation: r.Automation, Source: SourceReview, At: rn.Now(), Dedupe: "review-" + r.ID + "-" + r.State,
		Reason: "Review #" + r.ID + ": " + r.State})
}

func (rn *Runner) findReview(id string) int {
	for i, r := range rn.Store.Reviews {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// trimReviews keeps pending reviews and the latest decided ones.
func (rn *Runner) trimReviews() {
	for len(rn.Store.Reviews) > maxReviews {
		i := 0
		for i < len(rn.Store.Reviews) && rn.Store.Reviews[i].State == ReviewPending {
			i++
		}
		if i == len(rn.Store.Reviews) {
			return
		}
		rn.Store.Reviews = append(rn.Store.Reviews[:i], rn.Store.Reviews[i+1:]...)
	}
}

// PendingReviews are an automation's reviews waiting for the person, or all
// of them for id "".
func (s Store) PendingReviews(id string) []Review {
	var out []Review
	for _, r := range s.Reviews {
		if r.State == ReviewPending && (id == "" || r.Automation == id) {
			out = append(out, r)
		}
	}
	return out
}

// GrantsOf are an automation's grants that have not expired.
func (s Store) GrantsOf(id string, now time.Time) []Grant {
	var out []Grant
	for _, g := range s.Grants {
		if g.Automation == id && now.Before(g.Expires) {
			out = append(out, g)
		}
	}
	return out
}

// reviewNote tells a run the decisions on its automation's reviews that no
// run has been told yet, and marks them told.
func (rn *Runner) reviewNote(id string) string {
	var b strings.Builder
	for i := range rn.Store.Reviews {
		r := &rn.Store.Reviews[i]
		if r.Automation != id || r.State == ReviewPending || r.Delivered {
			continue
		}
		r.Delivered = true
		fmt.Fprintf(&b, "\n- #%s `%s`: ", r.ID, r.Action)
		switch {
		case r.State == ReviewApproved && r.Scope == ScopeProject:
			fmt.Fprintf(&b, "approved, with actions of the same kind (%s) in this project for 7 days. Run it again now; Miya will allow it.", r.Key)
		case r.State == ReviewApproved && r.Scope == ScopeKind:
			fmt.Fprintf(&b, "approved, with actions of the same kind (%s) for 7 days. Run it again now; Miya will allow it.", r.Key)
		case r.State == ReviewApproved:
			b.WriteString("approved once. Run exactly the same command again now; Miya will allow it.")
		case r.State == ReviewDenied:
			b.WriteString("denied. Do not do it; carry on without it.")
		default:
			b.WriteString("the user did not decide within 7 days; treat it as denied.")
		}
		if r.Note != "" {
			fmt.Fprintf(&b, " The user added: %s", r.Note)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nThe user decided on actions this automation sent for review:" + b.String()
}

// ProjectAllows reports whether a grant for project allows a command an
// ordinary conversation there asks to run. Only commands that reach outside
// are looked up: anything else is the person's to answer as before.
func (rn *Runner) ProjectAllows(project, command string) bool {
	if project == "" || !approval.OutsideCommand(command) {
		return false
	}
	return rn.useGrant("", project, command, approval.ActionKey(command), rn.Now())
}

func samePath(a, b string) bool {
	return a != "" && filepath.Clean(a) == filepath.Clean(b)
}
