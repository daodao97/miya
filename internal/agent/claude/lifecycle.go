package claude

import (
	"fmt"
	"strings"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	claudeprojection "miya/internal/agent/claude/projection"
	"miya/internal/chat/model"
	"miya/internal/chat/subagent"
	dg "miya/internal/delegation"
	"miya/internal/interaction"
)

func (b Adapter) Finish(id int, cwd string, err error) {
	c := b.host
	defer func() {
		t := c.ThreadWithID(id)
		state := dg.Interrupted
		if !c.Cancelled(err) && !t.StopRequested && (err != nil || t.Status != "") {
			state = dg.Failed
		}
		if c.FinishPausedDelivery(t.ExecutionID, err) {
			return
		}
		c.ManagedResult(id, state, "")
	}()
	// The run's conversation may be in a background project.
	here := c.ThreadWorkspace(id) == cwd
	if here {
		sid := c.ThreadWithID(id).ServerID
		go func() {
			path := transcript.Find(cwd, sid)
			if path == "" {
				return
			}
			session, ok := transcript.Summarize(path)
			if !ok {
				return
			}
			c.Dispatch(func() {
				if c.ThreadWorkspace(id) != cwd {
					return
				}
				c.Update(id, func(t *model.Thread) {
					if t.ServerID == sid {
						t.Title = session.Title
					}
				})
			})
		}()
	}
	c.SetRun(id, nil)
	if !here {
		return
	}
	c.Update(id, func(t *model.Thread) {
		t.Busy = false
		t.TaskStopping = false
		t.CompletedAtMs = time.Now().UnixMilli()
		// A result frame can report an API error even when the CLI exits
		// successfully. Keep that turn's error after the process is reaped.
		resultError := ""
		for i := len(t.Messages) - 1; i >= 0; i-- {
			if t.Messages[i].Role == "user" {
				break
			}
			if t.Messages[i].Role == "error" {
				resultError = t.Messages[i].Content
				break
			}
		}
		state := "interrupted"
		if t.StopRequested {
			state = "stopped"
		} else if err != nil || resultError != "" {
			state = "failed"
		}
		claude.StopTasks(t, state)
		agentState := subagent.Interrupted
		if state == "failed" {
			agentState = subagent.Failed
		}
		transcript.InterruptAgents(t, agentState)
		t.Status = ""
		if t.StopRequested {
			t.Status = "Stopped"
		} else if err != nil {
			t.Status = err.Error()
			t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: err.Error(), StartedAtMs: t.CompletedAtMs})
		} else {
			t.Status = resultError
		}
		for i := range t.Messages {
			if t.Messages[i].Status == "inProgress" {
				t.Messages[i].Status = "completed"
				if t.StopRequested {
					t.Messages[i].Status = "interrupted"
				} else if err != nil || resultError != "" {
					t.Messages[i].Status = "failed"
				}
			}
		}
		claudeprojection.CompleteClaudeTiming(t)
		claude.SaveHistory(*t)
	})
	pending := []interaction.Request{}
	prefix := fmt.Sprintf("\"claude:%d:", id)
	for _, a := range c.Approvals() {
		if !strings.HasPrefix(string(a.ID), prefix) {
			pending = append(pending, a)
		}
	}
	c.SetApprovals(pending)
}
