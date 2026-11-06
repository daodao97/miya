package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/appmodel"
	"miya/internal/codex"
	threadmodel "miya/internal/model"
	"miya/internal/prefs"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
)

// These commands are app-server operations, never slash text passed to a threadmodel.
// Capture settings and identity before leaving the UI thread.
// runThreadCommand runs a command in the window's open conversation.
func (c *Controller) RunThreadCommand(command string) string {
	id, message := c.ThreadCommand(c.Current().ID, c.CurrentAgent(), command, c.CurrentPermissionPolicy())
	if id != 0 {
		c.SetActive(id)
		c.SetDraft("")
	}
	return message
}

// threadCommand runs a command in a conversation, 0 for a new one of agent;
// it returns the conversation running it, or why it cannot run.
func (c *Controller) ThreadCommand(threadID int, agent, command, policy string) (int, string) {
	if c.Remote != nil {
		var r svcproto.CommandResult
		if err := c.Remote.Call("command", svcproto.Command{Thread: threadID, Agent: agent, Command: command, Policy: policy}, &r); err != nil {
			return 0, err.Error()
		}
		c.fastMode = r.FastMode
		return r.Thread, r.Message
	}
	if _, ok, _ := codex.ParseGoalCommand("/" + command); ok && (threadID == 0 && agent != appmodel.AgentClaude || threadID != 0 && threadmodel.ThreadAgent(c.ThreadWithID(threadID)) == appmodel.AgentCodex) {
		return c.CodexGoalCommand(threadmodel.SendRequest{ThreadID: threadID, Agent: appmodel.AgentCodex, Text: "/" + command, Policy: policy, Model: c.Model(), Effort: c.Effort()})
	}
	t := c.ThreadWithID(threadID)
	b := BackendOf(t)
	if t.ID == 0 {
		b = BackendFor(agent)
	}
	if command == "fast" || command == "fast on" || command == "fast off" {
		if b.ID() != appmodel.AgentCodex {
			return 0, tr("/fast is only supported by Codex.")
		}
		tier := prefs.Load("codexServiceTier")
		enabled := command == "fast on" || (command == "fast" && tier != "fast")
		tier = "default"
		if enabled {
			tier = "fast"
		}
		prefs.Save("codexServiceTier", tier)
		c.fastMode = enabled
		uiruntime.Invalidate()
		return t.ID, ""
	}
	if !b.Caps().ThreadCommands {
		return 0, b.Name() + tr(" does not support this command.")
	}
	if !c.Ready() || c.Client == nil {
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
		t = threadmodel.Thread{PermissionPolicy: policy, ID: c.NextID(), Title: tr("Code review"), Project: filepath.Base(c.Cwd), Age: "now", Loaded: true, HistoryLoaded: true}
		c.SetThreads(append([]threadmodel.Thread{t}, c.Threads()...))
	}
	client, id, cwd, model := c.Client, t.ID, c.Cwd, c.Model()
	c.Update(id, func(t *threadmodel.Thread) {
		t.Busy = true
		t.Status = tr("Starting /") + command + "…"
		t.Draft = ""
	})
	go func() {
		ctx, cancel := context.WithTimeout(c.Ctx, 45*time.Second)
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
						if c.Client == client {
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
				if c.Client != client {
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
