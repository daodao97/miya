package appmodel

import "miya/internal/chat/model"

// Waiting lists the conversations that wait for the person, by
// DesktopUnreadKey, each with what it waits for: the question its agent left
// in its state card, or nothing when an approval or a question of the agent
// is open in it.
type Waiting map[string]string

// Has reports whether t's conversation waits for the person.
func (w Waiting) Has(t model.Thread) bool {
	_, ok := w[DesktopUnreadKey(t)]
	return ok
}
