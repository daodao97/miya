package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	codex "miya/internal/agent/codex/native"
	threadmodel "miya/internal/model"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
)

func (b Adapter) RunCommand(threadID int, command, policy string) (int, string) {
	c := b.host
	if _, ok, _ := codex.ParseGoalCommand("/" + command); ok {
		return c.GoalCommand(threadmodel.SendRequest{ThreadID: threadID, Agent: b.ID(), Text: "/" + command, Policy: policy, Model: b.Model(), Effort: b.Effort()})
	}
	t := c.ThreadWithID(threadID)
	if command == "fast" || command == "fast on" || command == "fast off" {
		tier := prefs.Load("codexServiceTier")
		enabled := command == "fast on" || (command == "fast" && tier != "fast")
		tier = "default"
		if enabled {
			tier = "fast"
		}
		prefs.Save("codexServiceTier", tier)
		c.SetFastMode(enabled)
		uiruntime.Invalidate()
		return t.ID, ""
	}
	if !c.Snapshot().Ready || c.Snapshot().Client == nil {
		return 0, tr("Connect to Codex before running /") + command + "."
	}
	if t.Busy || t.Loading {
		return 0, tr("Wait for the current operation to finish before running /") + command + "."
	}
	if t.ReadOnly {
		return 0, tr("This conversation is read-only.")
	}
	if command == "compact" && t.ServerID == "" {
		return 0, tr("Start a conversation before using /compact.")
	}
	if command != "compact" && command != "review" {
		return 0, tr("Unsupported thread command.")
	}
	if t.ID == 0 {
		t = threadmodel.Thread{PermissionPolicy: policy, ID: c.NextID(), Title: tr("Code review"), Project: filepath.Base(c.Cwd()), Age: "now", Loaded: true, HistoryLoaded: true}
		c.SetThreads(append([]threadmodel.Thread{t}, c.Threads()...))
	}
	client, id, cwd, model := c.Snapshot().Client, t.ID, c.Cwd(), c.Snapshot().Model
	c.Update(id, func(t *threadmodel.Thread) {
		t.Busy = true
		t.Status = tr("Starting /") + command + "…"
		t.Draft = ""
	})
	go func() {
		ctx, cancel := context.WithTimeout(c.Context(), 45*time.Second)
		defer cancel()
		serverID := t.ServerID
		var err error
		if serverID == "" || !t.Resumed {
			method := "thread/start"
			params := map[string]any{"cwd": cwd, "sandbox": prefs.SandboxForPolicy(policy), "approvalPolicy": prefs.ApprovalForPolicy(policy), "approvalsReviewer": prefs.ReviewerForPolicy(policy)}
			if serverID != "" {
				method = "thread/resume"
				params["threadId"] = serverID
			}
			if model != "" {
				params["model"] = model
			}
			var result codex.ThreadResponse
			err = client.Call(ctx, method, params, &result)
			if err == nil {
				serverID = result.Thread.ID
				if serverID == "" {
					err = fmt.Errorf("%s", tr("Codex returned an empty thread ID"))
				} else {
					sid := serverID
					c.Dispatch(func() {
						if c.Snapshot().Client == client {
							c.Update(id, func(t *threadmodel.Thread) { t.ServerID = sid; t.Resumed = true })
						}
					})
				}
			}
		}
		if err == nil {
			method := "thread/compact/start"
			params := map[string]any{"threadId": serverID}
			if command == "review" {
				method = "review/start"
				params["delivery"] = "inline"
				params["target"] = map[string]string{"type": "uncommittedChanges"}
			}
			err = client.Call(ctx, method, params, nil)
		}
		if err != nil {
			c.Dispatch(func() {
				if c.Snapshot().Client != client {
					return
				}
				message := "/" + command + " failed: " + err.Error()
				c.Update(id, func(t *threadmodel.Thread) { t.Busy = false; t.Status = message; t.Draft = "/" + command })
				c.SetStatus(message)
				c.ReturnDraft(id, "/"+command, nil)
			})
		}
	}()
	return id, ""
}
