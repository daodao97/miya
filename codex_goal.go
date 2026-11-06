package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/codex"
	"miya/internal/textutil"
)

func (c *chatController) codexGoalCommand(req SendRequest) (int, string) {
	g, _, err := parseGoalCommand(req.Text)
	if err != nil {
		return 0, err.Error()
	}
	t := c.threadWithID(req.ThreadID)
	if req.ThreadID != 0 && t.ID == 0 {
		return 0, "This conversation no longer exists."
	}
	if t.ReadOnly || t.Loading || t.TaskPending {
		return 0, "Wait for the current operation to finish before managing goals."
	}
	if c.client == nil || !c.ready() {
		return 0, "Connect to Codex first."
	}
	if t.ID == 0 && g.Action != "set" {
		return 0, "This conversation has no goal. Use /goal <goal> to create one."
	}
	if len(req.Attachments) > 0 {
		return 0, "Send attachments separately before setting a goal."
	}
	policy := req.Policy
	if policy == "" {
		policy = c.permissionForThread(t)
	}
	if t.ID == 0 {
		cwd := textutil.FirstNonEmpty(req.Project, c.cwd)
		t = Thread{ID: c.nextID(), Agent: agentCodex, PermissionPolicy: policy,
			Title: conversationTitle("", g.Objective, ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.addThread(cwd, t)
	}
	client, id, cwd := c.client, t.ID, c.threadWorkspace(t.ID)
	cGoalRevision := t.GoalRevision
	c.update(id, func(t *Thread) { t.TaskPending = true })
	go func() {
		ctx, cancel := context.WithTimeout(c.parentContext(), 45*time.Second)
		defer cancel()
		serverID := t.ServerID
		var err error
		if serverID == "" || !t.Resumed {
			method := "thread/start"
			params := map[string]any{"cwd": cwd, "sandbox": sandboxForPermissionPolicy(policy), "approvalPolicy": approvalForPermissionPolicy(policy), "approvalsReviewer": reviewerForPermissionPolicy(policy)}
			if serverID != "" {
				method, params["threadId"] = "thread/resume", serverID
			}
			if req.Model != "" {
				params["model"] = req.Model
			}
			var response codex.ThreadResponse
			err = client.Call(ctx, method, params, &response)
			if err == nil {
				serverID = response.Thread.ID
				if serverID == "" {
					err = fmt.Errorf("Codex returned an empty conversation ID")
				} else {
					sid := serverID
					c.dispatch(func() { c.update(id, func(t *Thread) { t.ServerID, t.Resumed = sid, true }) })
				}
			}
		}
		var response codex.GoalResponse
		if err == nil {
			method := "thread/goal/get"
			params := map[string]any{"threadId": serverID}
			if g.Action == "clear" {
				method = "thread/goal/clear"
			} else if g.Action != "" {
				method, params["origin"] = "thread/goal/set", "user"
				switch g.Action {
				case "pause":
					params["status"] = "paused"
				case "resume":
					params["status"] = "active"
				case "set":
					params["objective"], params["status"] = g.Objective, "active"
					if g.Budget != nil {
						params["tokenBudget"] = *g.Budget
					}
				}
			}
			err = client.Call(ctx, method, params, &response)
		}
		c.dispatch(func() {
			if c.client != client {
				c.update(id, func(t *Thread) { t.TaskPending = false })
				c.returnDraft(id, req.Text, req.Attachments)
				return
			}
			c.update(id, func(t *Thread) {
				t.TaskPending = false
				if err != nil {
					t.Status = "Goal operation failed: " + err.Error()
					t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: t.Status})
					return
				}
				if t.GoalRevision == cGoalRevision {
					applyCodexGoal(t, response.Goal)
				}
			})
			if err != nil {
				c.returnDraft(id, req.Text, req.Attachments)
			}
		})
	}()
	return id, ""
}

// Fetch separately from history so a goal query cannot delay history loading
// and let an older turn snapshot overwrite an automatic continuation.
func (c *chatController) refreshCodexGoal(id int) {
	t, client := c.threadWithID(id), c.client
	if client == nil || t.ServerID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.parentContext(), 10*time.Second)
		defer cancel()
		var response codex.GoalResponse
		if client.Call(ctx, "thread/goal/get", map[string]any{"threadId": t.ServerID}, &response) != nil {
			return
		}
		c.dispatch(func() {
			if c.client != client {
				return
			}
			c.update(id, func(current *Thread) {
				if current.ServerID == t.ServerID && current.GoalRevision == t.GoalRevision {
					applyCodexGoal(current, response.Goal)
				}
			})
		})
	}()
}
