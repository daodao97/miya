// Package notification tells people about conversations that finished or
// wait for them: the text of desktop and phone notices, phone push
// registrations, and the unread state that keeps phone badges in step.
package notification

import (
	"fmt"
	"strconv"

	"miya/internal/chat/model"
	"miya/locales"
)

// Notice is one notification about a conversation.
type Notice struct{ ID, Thread, Title, Body, ServerID, Agent, Project string }

// Completion is the notice for a conversation that just finished, if it
// finished on its own.
func Completion(old, next model.Thread) (Notice, bool) {
	if !old.Busy || next.Busy || next.CompletedAtMs == 0 || next.StoppedByUser() {
		return Notice{}, false
	}
	title := tr("Miya · Completed")
	if next.Status != "" {
		title = tr("Miya · Failed")
	}
	return Notice{ID: fmt.Sprintf("miya-%d-%d", next.ID, next.CompletedAtMs), Thread: strconv.Itoa(next.ID), Title: title, Body: CompletionBody(old, next)}, true
}

func tr(s string) string { return locales.T(s) }
