package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/prefs"
	"miya/internal/textutil"
)

func (c *Controller) CodexGoalCommand(req model.SendRequest) (int, string) {
	g, _, err := codex.ParseGoalCommand(req.Text)
	if err != nil {
		return 0, err.Error()
	}
	t := c.ThreadWithID(req.ThreadID)
	if req.ThreadID != 0 && t.ID == 0 {
		return 0, "This conversation no longer exists."
	}
	if t.ReadOnly || t.Loading || t.TaskPending {
		return 0, "Wait for the current operation to finish before managing goals."
	}
	if c.Client == nil || !c.Ready() {
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
		cwd := textutil.FirstNonEmpty(req.Project, c.Cwd)
		t = model.Thread{ID: c.NextID(), Agent: appmodel.AgentCodex, PermissionPolicy: policy,
			Title: msgfmt.ConversationTitle("", g.Objective, ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.AddThread(cwd, t)
	}
	client, id, cwd := c.Client, t.ID, c.ThreadWorkspace(t.ID)
	cGoalRevision := t.GoalRevision
	c.Update(id, func(t *model.Thread) { t.TaskPending = true })
	go func() {
		ctx, cancel := context.WithTimeout(c.parentContext(), 45*time.Second)
		defer cancel()
		serverID := t.ServerID
		var err error
		if serverID == "" || !t.Resumed {
			method := "thread/start"
			params := map[string]any{"cwd": cwd, "sandbox": prefs.SandboxForPolicy(policy), "approvalPolicy": prefs.ApprovalForPolicy(policy), "approvalsReviewer": prefs.ReviewerForPolicy(policy)}
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
					c.Dispatch(func() { c.Update(id, func(t *model.Thread) { t.ServerID, t.Resumed = sid, true }) })
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
		c.Dispatch(func() {
			if c.Client != client {
				c.Update(id, func(t *model.Thread) { t.TaskPending = false })
				c.ReturnDraft(id, req.Text, req.Attachments)
				return
			}
			c.Update(id, func(t *model.Thread) {
				t.TaskPending = false
				if err != nil {
					t.Status = "Goal operation failed: " + err.Error()
					t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: t.Status})
					return
				}
				if t.GoalRevision == cGoalRevision {
					model.ApplyCodexGoal(t, response.Goal)
				}
			})
			if err != nil {
				c.ReturnDraft(id, req.Text, req.Attachments)
			}
		})
	}()
	return id, ""
}

// Fetch separately from history so a goal query cannot delay history loading
// and let an older turn snapshot overwrite an automatic continuation.
func (c *Controller) refreshCodexGoal(id int) {
	t, client := c.ThreadWithID(id), c.Client
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
		c.Dispatch(func() {
			if c.Client != client {
				return
			}
			c.Update(id, func(current *model.Thread) {
				if current.ServerID == t.ServerID && current.GoalRevision == t.GoalRevision {
					model.ApplyCodexGoal(current, response.Goal)
				}
			})
		})
	}()
}
