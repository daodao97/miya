package controller

import (
	appmodel "miya/internal/appmodel"
	model "miya/internal/model"
	threadlist "miya/internal/threadlist"
	filepath "path/filepath"
	sort "sort"
)

// loadClaudeSessions merges the CLI's sessions of the workspace into its
// conversations, without the messages, which load as one is opened.
func (c *Controller) loadClaudeSessions() {
	cwd := c.Cwd
	go c.loadClaudeSessionsAt(cwd)
}

// loadClaudeSessionsSync reads the sessions on the calling goroutine.
func (c *Controller) LoadClaudeSessionsSync() {
	c.loadClaudeSessionsAt(c.Cwd)
}

func (c *Controller) loadClaudeSessionsAt(cwd string) {
	{
		sessions := appmodel.ListClaudeSessions(cwd)
		if len(sessions) == 0 {
			return
		}
		c.Dispatch(func() {
			if c.Cwd != cwd {
				return
			}
			items := append([]appmodel.Thread(nil), c.Threads()...)
			known := map[string]int{}
			for i, t := range items {
				if model.ThreadAgent(t) == appmodel.AgentClaude && t.ServerID != "" {
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
						items[i].HistoryLoaded = items[i].HistoryLoaded && items[i].ID == c.Active()
					}
					continue
				}
				items = append(items, appmodel.Thread{
					ID: c.NextID(), Agent: "claude-code", ServerID: s.ID, Title: s.Title,
					Project: filepath.Base(cwd), Age: s.UpdatedAt.Format("Jan 2"), LastActivityAt: at, Resumed: true,
				})
			}
			sort.SliceStable(items, func(i, j int) bool { return items[i].LastActivityAt > items[j].LastActivityAt })
			if len(items) > maxVisibleThreads {
				items = items[:maxVisibleThreads]
			}
			c.SetThreads(items)
			if c.ProjectCache == nil {
				c.ProjectCache = map[string][]appmodel.Thread{}
			}
			c.ProjectCache[cwd] = append([]appmodel.Thread(nil), items...)
			threadlist.Persist(c.ProjectCache)
			c.RefreshProjectOrder()
		})
	}
}
