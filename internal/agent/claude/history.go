package claude

import (
	"encoding/json"
	"os"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	"miya/internal/model"
)

func (b Adapter) LoadHistory(t model.Thread) {
	c := b.host
	if t.HistoryLoaded || t.Loading {
		return
	}
	cwd := c.ThreadWorkspace(t.ID)
	c.Update(t.ID, func(t *model.Thread) { t.Loading = true })
	done := make(chan struct{})
	c.HistoryLoading(done)
	go func() {
		defer close(done)
		// Claude Code's own record is the truth, as the CLI may have continued
		// the session; the app's copy remains for sessions it cannot find.
		var stored model.Thread
		var err error
		if path := transcript.Find(cwd, t.ServerID); path != "" {
			stored.Messages, err = transcript.Parse(path)
			stored.Resumed = true
		}
		if len(stored.Messages) == 0 {
			var data []byte
			data, err = os.ReadFile(claude.HistoryPath(t.ServerID))
			if err == nil {
				err = json.Unmarshal(data, &stored)
			}
		}
		c.Dispatch(func() {
			if c.ThreadWorkspace(t.ID) != cwd || c.ThreadWithID(t.ID).ServerID != t.ServerID {
				return
			}
			c.Update(t.ID, func(t *model.Thread) {
				t.Loading = false
				t.HistoryLoaded = true
				if err != nil {
					t.Status = "Local history is unavailable. Send a message to continue this conversation."
					return
				}
				t.Messages = stored.Messages
				for i := range t.Messages {
					t.Messages[i].ID = c.NextID()
				}
				t.Resumed = stored.Resumed
				t.Loaded = true
			})
			c.DrainQueue(t.ID)
		})
	}()
}
