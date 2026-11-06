package main

import (
	"path/filepath"
	"sort"

	"miya/internal/model"
	"miya/internal/threadlist"
)

// Claude Code keeps each session as JSON lines under
// ~/.claude/projects/<the working directory, encoded>/<session id>.jsonl.
// Reading them shows conversations started with the CLI, as Waku does; the
// app resumes them with --resume, as its own.

// loadClaudeSessions merges the CLI's sessions of the workspace into its
// conversations, without the messages, which load as one is opened.
func (c *chatController) loadClaudeSessions() {
	cwd := c.cwd
	go c.loadClaudeSessionsAt(cwd)
}

// loadClaudeSessionsSync reads the sessions on the calling goroutine.
func (c *chatController) loadClaudeSessionsSync() {
	c.loadClaudeSessionsAt(c.cwd)
}

func (c *chatController) loadClaudeSessionsAt(cwd string) {
	{
		sessions := listClaudeSessions(cwd)
		if len(sessions) == 0 {
			return
		}
		c.dispatch(func() {
			if c.cwd != cwd {
				return
			}
			items := append([]Thread(nil), c.threads()...)
			known := map[string]int{}
			for i, t := range items {
				if model.ThreadAgent(t) == agentClaude && t.ServerID != "" {
					known[t.ServerID] = i
				}
			}
			for _, s := range sessions {
				at := s.UpdatedAt.UnixMilli()
				if i, ok := known[s.ID]; ok {
					if !items[i].Busy {
						items[i].Title = s.Title
					}
					// The CLI may have continued it since: show it where it now sorts.
					if at > items[i].LastActivityAt && !items[i].Busy {
						items[i].LastActivityAt = max(items[i].LastActivityAt, at)
						items[i].Age = s.UpdatedAt.Format("Jan 2")
						items[i].HistoryLoaded = items[i].HistoryLoaded && items[i].ID == c.active()
					}
					continue
				}
				items = append(items, Thread{
					ID: c.nextID(), Agent: "claude-code", ServerID: s.ID, Title: s.Title,
					Project: filepath.Base(cwd), Age: s.UpdatedAt.Format("Jan 2"), LastActivityAt: at, Resumed: true,
				})
			}
			sort.SliceStable(items, func(i, j int) bool { return items[i].LastActivityAt > items[j].LastActivityAt })
			if len(items) > maxVisibleThreads {
				items = items[:maxVisibleThreads]
			}
			c.setThreads(items)
			if c.projectCache == nil {
				c.projectCache = map[string][]Thread{}
			}
			c.projectCache[cwd] = append([]Thread(nil), items...)
			threadlist.Persist(c.projectCache)
			c.refreshProjectOrder()
		})
	}
}
