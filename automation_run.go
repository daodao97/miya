package main

import (
	"encoding/json"
	"fmt"

	"miya/internal/automation"
	"miya/internal/codex"
	"miya/internal/notification"
)

// automationHost is the controller as the automation runner sees it.
type automationHost struct{ c *chatController }

func (h automationHost) Dispatch(f func())                      { h.c.dispatch(f) }
func (h automationHost) Submit(r SendRequest) int               { return h.c.submit(r) }
func (h automationHost) Thread(id int) Thread                   { return h.c.threadWithID(id) }
func (h automationHost) UpdateThread(id int, f func(*Thread))   { h.c.update(id, f) }
func (h automationHost) AwaitsUser(t Thread) bool               { return h.c.threadAwaitsUser(t) }
func (h automationHost) Outcome(id int) (string, string)        { return h.c.handoffState(id) }
func (h automationHost) AgentName(agent string) string          { return backendFor(agent).Name() }
func (h automationHost) AutoReport(t Thread) *automation.Report { return autoRunReport(t) }
func (h automationHost) Deliver(n notification.Notice)          { h.c.deliverNotice(n, "completion") }
func (h automationHost) SyncLoginItem(enabled bool)             { syncAutomationLaunchAgent(enabled) }

// startAutomations loads the automations and, with schedule, checks them
// every automation.TickInterval from now on.
func (c *chatController) startAutomations(path string, schedule bool) {
	c.auto = automation.NewRunner(automationHost{c}, path)
	done := make(<-chan struct{})
	if c.ctx != nil {
		done = c.ctx.Done()
	}
	c.auto.Start(done, schedule)
}

// approvalThread is the conversation an approval waits in, Codex's by its
// thread id and Claude Code's by the conversation in its request id.
func (c *chatController) approvalThread(a codex.Approval) (Thread, bool) {
	if claudeApproval(a) {
		var s string
		_ = json.Unmarshal(a.ID, &s)
		var id int
		if _, err := fmt.Sscanf(s, "claude:%d:", &id); err != nil {
			return Thread{}, false
		}
		t := c.threadWithID(id)
		return t, t.ID != 0
	}
	if a.ThreadID == "" {
		return Thread{}, false
	}
	for _, t := range c.threads() {
		if t.ServerID == a.ThreadID {
			return t, true
		}
	}
	for path, threads := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for _, t := range threads {
			if t.ServerID == a.ThreadID {
				return t, true
			}
		}
	}
	return Thread{}, false
}

// threadAwaitsUser reports whether a conversation waits for an approval or
// an answer.
func (c *chatController) threadAwaitsUser(t Thread) bool {
	if c.approvals == nil {
		return false
	}
	for _, a := range c.approvals() {
		if at, ok := c.approvalThread(a); ok && at.ID == t.ID {
			return true
		}
	}
	return false
}
