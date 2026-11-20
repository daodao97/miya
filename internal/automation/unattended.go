package automation

import (
	"fmt"
	"strings"

	"miya/internal/approval"
	"miya/internal/textutil"
)

// Ask is an approval request from the conversation of an automation run,
// as the agent sent it (docs/action-review.md).
type Ask = approval.Request

// Answer is what Miya answers an Ask with when no person is asked.
type Answer struct {
	// Decided is false when the request waits for a person as before.
	Decided bool
	Allow   bool
	// Message tells the agent why it was refused and what to do instead.
	Message string
}

// maxRefused is how many refused actions a run keeps.
const maxRefused = 20

// Unattended answers an approval request from the conversation thread when
// it belongs to a running automation with an access level. A refusal is
// recorded on the run.
func (rn *Runner) Unattended(thread int, ask Ask) Answer {
	if thread == 0 {
		return Answer{}
	}
	for id, r := range rn.Active {
		if r.Thread != thread {
			continue
		}
		i, ok := rn.Store.Find(id)
		if !ok || rn.Store.Automations[i].Access == "" {
			return Answer{}
		}
		a := rn.Store.Automations[i]
		dir := textutil.FirstNonEmpty(r.dir, a.Project)
		answer, what := decide(a.Access, dir, ask)
		if !answer.Decided && a.Access == AccessExternal && what != "" {
			return rn.review(a, r, dir, what)
		}
		if answer.Decided && !answer.Allow {
			rn.updateRun(id, r, func(run *Run) {
				if len(run.Refused) < maxRefused {
					run.Refused = append(run.Refused, textutil.ClipRunes(what, 200))
				}
			})
		}
		return answer
	}
	return Answer{}
}

// decide answers ask for an automation with access, whose run works in
// dir, and says what was asked for.
func decide(access Access, dir string, ask Ask) (Answer, string) {
	local, what := approval.Classify(dir, ask)
	if access == AccessFull {
		// Runs with full access rarely ask; what still does goes on.
		return Answer{Decided: true, Allow: true}, what
	}
	if ask.Tool == "ExitPlanMode" && access == AccessRead {
		return Answer{Decided: true, Message: "This automation is read only (access: read), so there is no plan to carry out. Report what you found with report_result, and if a change should be made, set needs_user with user_action saying which."}, "leave plan mode"
	}
	if strings.HasPrefix(ask.Tool, "mcp__miya__") {
		return Answer{Decided: true, Allow: true}, what
	}
	if access == AccessRead && ask.Tool == "Bash" && approval.ReadOnlyCommand(approval.BashCommand(ask)) {
		return Answer{Decided: true, Allow: true}, what
	}
	switch {
	case local && access != AccessRead:
		return Answer{Decided: true, Allow: true}, what
	case local:
		return Answer{Decided: true, Message: fmt.Sprintf("Miya refused %s: this automation is read only (access: read). Do not try another way to change anything. Report what you found with report_result, and if a change should be made, set needs_user with user_action saying which.", what)}, what
	case access == AccessExternal:
		// Unattended sends it for review.
		return Answer{}, what
	}
	return Answer{Decided: true, Message: fmt.Sprintf("Miya refused %s: this automation may not act outside the project (access: %s). Do not try another way to do it. Finish the work that does not depend on it, and set needs_user in report_result with user_action saying what should be done.", what, access)}, what
}
