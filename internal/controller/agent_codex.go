package controller

import (
	"miya/internal/appmodel"
	dg "miya/internal/delegation"
	"miya/internal/model"
)

func (c *Controller) resetCodexSessions() {
	for _, t := range c.AllThreads() {
		if model.ThreadAgent(t) == appmodel.AgentCodex && t.ExecutionID != "" {
			c.ManagedEvent(t.ExecutionID, dg.Event{State: dg.NeedsAttention, Result: dg.Result{Error: "Codex connection ended before execution could be confirmed"}})
		}
	}
	for path, threads := range c.ProjectCache {
		threads = append([]model.Thread(nil), threads...)
		for i := range threads {
			model.ResetCodexSession(&threads[i])
		}
		c.ProjectCache[path] = threads
	}
	threads := append([]model.Thread(nil), c.Threads()...)
	for i := range threads {
		model.ResetCodexSession(&threads[i])
	}
	c.SetThreads(threads)
	if c.ProjectCache != nil && c.Cwd != "" {
		c.ProjectCache[c.Cwd] = append([]model.Thread(nil), threads...)
	}
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}
