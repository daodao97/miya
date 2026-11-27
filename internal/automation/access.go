package automation

import (
	"fmt"

	agentpolicy "miya/internal/agent/policy"
)

// Access is what an automation's runs may do without a person
// (docs/action-review.md). Empty follows the permission picked in the
// window, as automations did before access levels.
type Access string

const (
	// AccessRead reads and plans; it changes no files.
	AccessRead Access = "read"
	// AccessEdit edits project files and runs local commands; Miya refuses
	// actions that reach outside.
	AccessEdit Access = "edit"
	// AccessExternal is AccessEdit, with each outside action sent to a
	// person for review.
	AccessExternal Access = "external"
	// AccessFull runs with full access and never waits for a person: the
	// person chose this automation to act on its own.
	AccessFull Access = "full"
)

// Policy is the permission policy its runs are sent with. Only AccessFull
// runs with full access; below it every request beyond the level reaches
// Miya.
func (a Access) Policy() string {
	switch a {
	case AccessFull:
		return "never"
	case AccessRead:
		return agentpolicy.Read
	case AccessEdit, AccessExternal:
		return agentpolicy.Edit
	}
	return ""
}

func (a Access) valid() error {
	switch a {
	case "", AccessRead, AccessEdit, AccessExternal, AccessFull:
		return nil
	}
	return fmt.Errorf("Unknown access %q: use read, edit, external or full", string(a))
}

// AccessText says what an automation's runs may do, for cards and diffs.
func AccessText(a Access) string {
	switch a {
	case AccessRead:
		return "read only, no file changes"
	case AccessEdit:
		return "edit project files; actions that reach outside are refused"
	case AccessExternal:
		return "edit project files; each action that reaches outside waits for your review"
	case AccessFull:
		return "full access; runs never wait for you"
	}
	return "the permission picked in the window"
}

// notifyText says when runs notify.
func notifyText(a Automation) string {
	if a.NotifyEveryRun {
		return "every run"
	}
	return "only runs with something new"
}

func stallText(a Automation) string {
	if a.StallAfter == 0 {
		return "never"
	}
	return fmt.Sprintf("after %d runs in a row with nothing new", a.StallAfter)
}

func recheckText(a Automation) string {
	if a.Recheck {
		return "in a clean checkout before merging"
	}
	return "no"
}

// accessRank orders the access levels from the narrowest.
var accessRank = map[Access]int{AccessRead: 1, AccessEdit: 2, AccessExternal: 3, AccessFull: 4}

// Narrower is the narrower of two access levels. Empty, which follows the
// window's choice, yields to the other.
func Narrower(a, b Access) Access {
	if a == "" {
		return b
	}
	if b == "" || accessRank[a] <= accessRank[b] {
		return a
	}
	return b
}
