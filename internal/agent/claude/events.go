package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"miya/internal/interaction"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/subagent"
	"miya/internal/textutil"
)

func (b Adapter) Handle(id int, f claude.Frame) {
	c := b.host
	if f.ParentToolUseID == "" && (f.Type == "assistant" || f.Type == "stream_event" || f.Type == "control_request") {
		c.ManagedStarted(id)
	}
	if f.Type == "assistant" || f.Type == "user" || f.Type == "system" || f.Type == "result" {
		c.NoteRuntime(c.ThreadWithID(id))
	}
	if f.Type == "system" && f.Subtype == "init" {
		c.NoteModel(f.Model)
		if f.Model != "" {
			c.Update(id, func(t *model.Thread) { t.LaunchModel = f.Model })
		}
	}
	if f.Type == "control_cancel_request" {
		rid, _ := json.Marshal(fmt.Sprintf("claude:%d:%s", id, f.RequestID))
		c.RemoveRequest(rid)
		return
	}

	if f.Type == "control_request" {
		rid, _ := json.Marshal(fmt.Sprintf("claude:%d:%s", id, f.RequestID))
		if f.Request.Subtype != "can_use_tool" {
			if run := c.Run(id); run != nil {
				go run.Reply(f.RequestID, map[string]any{"behavior": "deny", "message": "Unsupported request"})
			}
			return
		}
		// Phones route request cards by the session ID, including requests
		// from conversations in background projects.
		a := interaction.Request{ID: rid, ThreadID: c.ThreadWithID(id).ServerID, ItemID: f.Request.ToolUseID, Method: "claude/permission", Command: f.Request.ToolName, Reason: string(f.Request.Input), Payload: f.Request.Input}
		if f.Request.ToolName == "AskUserQuestion" {
			var input struct {
				Questions []struct {
					Question, Header string
					Options          []struct{ Label, Description string }
				}
			}
			_ = json.Unmarshal(f.Request.Input, &input)
			a.Method = "item/tool/requestUserInput"
			for _, q := range input.Questions {
				question := interaction.Question{ID: q.Question, Header: q.Header, Question: q.Question}
				for _, o := range q.Options {
					question.Options = append(question.Options, struct {
						Label       string `json:"label"`
						Description string `json:"description"`
					}{Label: o.Label, Description: o.Description})
				}
				a.Questions = append(a.Questions, question)
			}
		}
		c.SetApprovals(append(append([]interaction.Request(nil), c.Approvals()...), a))
		return
	}
	// Set only by a successful top-level result. The thread's Status is display
	// text that stays "running" until the process exits for ordinary turns, so
	// it cannot decide whether this turn completed.
	memoryTurnDone := false
	c.Update(id, func(t *model.Thread) {
		defer func() { transcript.ApplyAgentTasks(t, f.AgentTasks) }()
		now := time.Now().UnixMilli()
		t.LastOutputAtMs = now
		if f.SchedulesKnown {
			claude.ApplySchedules(t, f.Schedules)
		}
		if f.Type == "command_lifecycle" && f.CommandState == "started" {
			if !t.Busy {
				msgfmt.ResetTurnOutput(t)
				t.StartedAtMs = now
			}
			t.Busy, t.StopRequested = true, false
			t.CompletedAtMs = 0
			t.Status = "Claude Code is running…"
		}
		if f.SessionID != "" {
			t.ServerID = f.SessionID
			t.Resumed = true
		}
		if f.Type == "system" && strings.HasPrefix(f.Subtype, "task_") {
			transcript.TaskFrame(t, f)
			return
		}
		// A sub-agent's own frames are steps of its Task call, not messages
		// of the conversation.
		if f.ParentToolUseID != "" {
			if f.Type == "assistant" {
				transcript.SubAgentStep(t, f.ParentToolUseID, f.Message.Content)
			}
			return
		}
		switch f.Type {
		case "stream_event":
			var e struct {
				Type    string
				Index   int
				Message claude.Message
				Delta   struct{ Type, Text string }
				Usage   json.RawMessage
			}
			if json.Unmarshal(f.Event, &e) != nil {
				return
			}
			if e.Type == "message_start" {
				t.ClaudeMessageID = e.Message.ID
				msgfmt.RecordClaudeOutput(t, e.Message.ID, e.Message.Usage)
			}
			if e.Type == "message_delta" {
				msgfmt.RecordClaudeOutput(t, t.ClaudeMessageID, e.Usage)
			}
			if e.Type == "content_block_delta" && e.Delta.Type == "text_delta" {
				key := fmt.Sprintf("%s:%d", t.ClaudeMessageID, e.Index)
				for i := range t.Messages {
					if t.Messages[i].ItemID == key {
						t.Messages[i].Content += e.Delta.Text
						return
					}
				}
				t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "assistant", Kind: "agentMessage", ItemID: key, Content: e.Delta.Text, SentAtMs: now, StartedAtMs: now})
			}
		case "assistant":
			msgfmt.RecordClaudeOutput(t, f.Message.ID, f.Message.Usage)
			for i, b := range f.Message.Content {
				if b.Type == "text" {
					key := claude.TextKey(t, f.Message.ID, i, b.Text)
					c.Upsert(t, model.Message{Role: "assistant", Kind: "agentMessage", ItemID: key, Content: b.Text, SentAtMs: now, StartedAtMs: now})
				}
				if b.Type == "tool_use" {
					m := model.Message{Role: "tool", Kind: "claudeTool", ItemID: b.ID, Content: b.Name + "\n" + string(b.Input), Status: "inProgress", Agents: transcript.SubAgents(b.ID, b.Name, b.Input, "inProgress")}
					subagent.Keep(t, &m)
					c.Upsert(t, m)
				}
			}
		case "user":
			for _, b := range f.Message.Content {
				if b.Type == "tool_result" {
					for i := range t.Messages {
						if t.Messages[i].ItemID == b.ToolUseID {
							t.Messages[i].Status = "completed"
							if b.IsError {
								t.Messages[i].Status = "failed"
							}
							output := transcript.VisibleText(transcript.Blocks(b.Content))
							if shown := transcript.ToolOutput(t.Messages[i], output); shown != "" {
								t.Messages[i].Content += "\n" + textutil.Bounded(shown)
							}
							transcript.FinishSubAgents(&t.Messages[i], output, f.ToolUseResult)
						}
					}
				}
			}
		case "result":
			t.TaskStopping = false
			// Interrupting Claude Code can itself produce an error result (for
			// example an ede_diagnostic with stop_reason=tool_use).
			// finishClaude will finalize it as a requested stop.
			if t.StopRequested {
				return
			}
			hadOutput := claude.TurnHasOutput(t.Messages)
			emptyResult := !f.IsError && !f.KeepingAlive && !hadOutput && strings.TrimSpace(f.Result) == ""
			if f.IsError || emptyResult {
				claude.StopTasks(t, "failed")
			}
			if f.KeepingAlive {
				t.Busy = f.WaitingForAgents
				if f.WaitingForAgents {
					t.CompletedAtMs = 0
					t.Status = "Waiting for subagents to finish…"
				} else {
					t.CompletedAtMs = now
					t.Status = ""
				}
			}
			if f.IsError || emptyResult {
				message := strings.Join(f.Errors, "\n")
				if message == "" {
					message = f.Result
				}
				if message == "" {
					message = "Claude Code run failed"
				}
				if emptyResult {
					message = "Claude Code finished the turn without returning a response."
				}
				message = claude.ErrorMessage(message, hadOutput, c.Snapshot().Command != "")
				t.Status = message
				t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: message, StartedAtMs: now})
			} else if !hadOutput && strings.TrimSpace(f.Result) != "" {
				// Some CLI transports only provide the final result text.
				t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "assistant", Kind: "agentMessage", Content: f.Result, SentAtMs: now, StartedAtMs: now})
			}
			if f.KeepingAlive && !f.WaitingForAgents {
				msgfmt.CompleteClaudeTiming(t)
				claude.SaveHistory(*t)
			}
			memoryTurnDone = !f.IsError && !emptyResult && !f.WaitingForAgents
		}
	})
	if memoryTurnDone {
		c.Memories().CompletedTurn(id, f.UUID)
	}
	if f.Type == "result" && f.ParentToolUseID == "" && !f.WaitingForAgents {
		state := dg.Completed
		if c.ThreadWithID(id).StopRequested {
			state = dg.Interrupted
		} else if f.IsError {
			state = dg.Failed
		}
		c.ManagedResult(id, state, f.UUID)
	}
}
