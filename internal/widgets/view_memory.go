package widgets

import (
	"time"
)

func MemoryKindLabel(kind string) string {
	switch kind {
	case "preference":
		return tr("Preference")
	case "fact":
		return tr("Fact")
	case "progress":
		return tr("Progress")
	}
	return kind
}

func MemoryStateLabel(state string) string {
	switch state {
	case "active":
		return tr("Active")
	case "expired":
		return tr("Expired")
	case "forgotten":
		return tr("Forgotten")
	case "promoted":
		return tr("Promoted")
	case "superseded":
		return tr("Updated")
	case "conflicted":
		return tr("Conflict · Needs verification")
	}
	return state
}

func MemoryTime(t time.Time) string {
	if t.IsZero() {
		return tr("Not set")
	}
	return t.Local().Format("2006-01-02 15:04")
}
