package widgets

import (
	"miya/internal/runtime/presentation"
	"miya/internal/runtime/tracker"
)

func RuntimeStatus(status string) string { return presentation.RuntimeStatus(status) }
func RuntimeKind(kind string) string     { return presentation.RuntimeKind(kind) }
func RuntimeObservationLost(result *tracker.ConversationRuntime) {
	presentation.RuntimeObservationLost(result)
}
