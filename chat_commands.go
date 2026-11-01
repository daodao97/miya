package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/codex"
)

// These commands are app-server operations, never slash text passed to a model.
// Capture settings and identity before leaving the UI thread.
// runThreadCommand runs a command in the window's open conversation.
func (c *chatController) runThreadCommand(command string) string {
	id, message := c.threadCommand(c.current().ID, c.currentAgent(), command, c.currentPermissionPolicy())
	if id != 0 {
		c.setActive(id)
		c.setDraft("")
	}
	return message
}

// threadCommand runs a command in a conversation, 0 for a new one of agent;
// it returns the conversation running it, or why it cannot run.
func (c *chatController) threadCommand(threadID int, agent, command, policy string) (int, string) {
	if c.remote != nil {
		var r svcCommandResult
		if err := c.remote.call("command", svcCommand{Thread: threadID, Agent: agent, Command: command, Policy: policy}, &r); err != nil {
			return 0, err.Error()
		}
		c.fastMode = r.FastMode
		return r.Thread, r.Message
	}
	if _, ok, _ := parseGoalCommand("/" + command); ok && (threadID == 0 && agent != agentClaude || threadID != 0 && threadAgent(c.threadWithID(threadID)) == agentCodex) {
		return c.codexGoalCommand(sendRequest{ThreadID: threadID, Agent: agentCodex, Text: "/" + command, Policy: policy, Model: c.model(), Effort: c.effort()})
	}
	t := c.threadWithID(threadID)
	b := backendOf(t)
	if t.ID == 0 {
		b = backendFor(agent)
	}
	if command == "fast" || command == "fast on" || command == "fast off" {
		if b.ID() != agentCodex {
			return 0, "/fast 仅支持 Codex。"
		}
		tier := loadSetting("codexServiceTier")
		enabled := command == "fast on" || (command == "fast" && tier != "fast")
		tier = "default"
		if enabled {
			tier = "fast"
		}
		persistSetting("codexServiceTier", tier)
		c.fastMode = enabled
		invalidate()
		return t.ID, ""
	}
	if !b.Caps().ThreadCommands {
		return 0, b.Name() + " 不支持此命令。"
	}
	if !c.ready() || c.client == nil {
		return 0, "Connect to Codex before running /" + command + "."
	}
	if t.Busy || t.Loading {
		return 0, "Wait for the current operation to finish before running /" + command + "."
	}
	if t.ReadOnly {
		return 0, "This conversation is read-only."
	}
	if command == "compact" && t.ServerID == "" {
		return 0, "Start a conversation before using /compact."
	}
	if command != "compact" && command != "review" {
		return 0, "Unsupported thread command."
	}
	if t.ID == 0 {
		t = Thread{PermissionPolicy: policy, ID: c.nextID(), Title: "Code review", Project: filepath.Base(c.cwd), Age: "now", Loaded: true, HistoryLoaded: true}
		c.setThreads(append([]Thread{t}, c.threads()...))
	}
	client, id, cwd, model := c.client, t.ID, c.cwd, c.model()
	c.update(id, func(t *Thread) { t.Busy = true; t.Status = "Starting /" + command + "…"; t.Draft = "" })
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 45*time.Second)
		defer cancel()
		serverID := t.ServerID
		var err error
		if serverID == "" || !t.Resumed {
			method := "thread/start"
			params := map[string]any{"cwd": cwd, "sandbox": sandboxForPermissionPolicy(policy), "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy)}
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
					err = fmt.Errorf("Codex returned an empty thread ID")
				} else {
					sid := serverID
					c.dispatch(func() {
						if c.client == client {
							c.update(id, func(t *Thread) { t.ServerID = sid; t.Resumed = true })
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
			c.dispatch(func() {
				if c.client != client {
					return
				}
				message := "/" + command + " failed: " + err.Error()
				c.update(id, func(t *Thread) { t.Busy = false; t.Status = message; t.Draft = "/" + command })
				c.setStatus(message)
				c.returnDraft(id, "/"+command, nil)
			})
		}
	}()
	return id, ""
}
