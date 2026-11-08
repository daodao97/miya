package model

import agentmeta "miya/internal/agent/catalog"

// Resumed and writer ownership are valid only for the current app-server.
// Claude's Resumed instead describes its persisted CLI session.
func ResetConnectionSession(t *Thread) {
	if !agentmeta.For(ThreadAgent(*t)).ConnectionBoundSession {
		return
	}
	t.Resumed, t.ReadOnly = false, false
	t.NativeSandbox = nil
	t.Loaded, t.Loading, t.Busy, t.StopRequested = false, false, false, false
	t.TurnID = ""
	t.MemoryEpoch++
	if t.Status == "Working…" || t.Status == "Starting…" || t.Status == "Stopping…" {
		t.Status = ""
	}
}
