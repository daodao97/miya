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
)

// Policy is the permission policy its runs are sent with: never full
// access, so every request beyond it reaches Miya.
func (a Access) Policy() string {
	switch a {
	case AccessRead:
		return agentpolicy.Read
	case AccessEdit, AccessExternal:
		return agentpolicy.Edit
	}
	return ""
}

func (a Access) valid() error {
	switch a {
	case "", AccessRead, AccessEdit, AccessExternal:
		return nil
	}
	return fmt.Errorf("Unknown access %q: use read, edit or external", string(a))
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
