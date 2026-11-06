package model

// Resumed and writer ownership are valid only for the current app-server.
// Claude's Resumed instead describes its persisted CLI session.
func ResetCodexSession(t *Thread) {
	if ThreadAgent(*t) != AgentCodex {
		return
	}
	t.Resumed, t.ReadOnly = false, false
	t.Loaded, t.Loading, t.Busy, t.StopRequested = false, false, false, false
	t.TurnID = ""
	t.MemoryEpoch++
	if t.Status == "Working…" || t.Status == "Starting…" || t.Status == "Stopping…" {
		t.Status = ""
	}
}
