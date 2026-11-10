package pi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/textutil"
)

// Handle applies one pi event to its conversation.
func (b Adapter) Handle(id int, e pinative.Event) {
	c := b.host
	switch e.Type {
	case "agent_start":
		c.ManagedStarted(id)
		c.NoteRuntime(c.ThreadWithID(id))
	case "agent_end":
		b.endTurn(id, e.Messages)
		return
	}
	c.Update(id, func(t *model.Thread) {
		now := time.Now().UnixMilli()
		t.LastOutputAtMs = now
		switch e.Type {
		case "agent_start":
			t.Busy = true
			t.Status = tr("Pi is running…")
		case "message_start":
			if e.Message.Role == "assistant" {
				at := e.Message.Timestamp
				if at == 0 {
					at = now
				}
				t.ClaudeMessageID = fmt.Sprintf("pi:%d", at)
			}
		case "message_update":
			b.delta(t, e, now)
		case "message_end":
			if e.Message.Role == "assistant" {
				b.assistant(t, e.Message, now)
			}
		case "tool_execution_start":
			name, input := ToolCall(e.ToolName, e.Args)
			c.Upsert(t, model.Message{Role: "tool", Kind: "claudeTool", ItemID: e.ToolCallID, Content: name + "\n" + input, Status: "inProgress", SentAtMs: now, StartedAtMs: now})
		case "tool_execution_end":
			for i := range t.Messages {
				if t.Messages[i].ItemID != e.ToolCallID || t.Messages[i].Role != "tool" {
					continue
				}
				t.Messages[i].Status, t.Messages[i].CompletedAtMs = "completed", now
				if e.IsError {
					t.Messages[i].Status = "failed"
				}
				if e.Result != nil {
					if out := strings.TrimSpace(pinative.Text(e.Result.Content)); out != "" {
						t.Messages[i].Content += "\n" + textutil.Bounded(out)
					}
				}
			}
		case "auto_retry_start":
			t.Status = fmt.Sprintf(tr("Retrying (%d/%d)…"), e.Attempt, e.MaxAttempts)
		case "auto_retry_end", "compaction_end":
			t.Status = tr("Pi is running…")
		case "compaction_start":
			t.Status = tr("Compacting the conversation…")
		}
	})
}

func (b Adapter) delta(t *model.Thread, e pinative.Event, now int64) {
	c := b.host
	d := e.AssistantMessageEvent
	key := fmt.Sprintf("%s:%d", t.ClaudeMessageID, d.ContentIndex)
	switch d.Type {
	case "text_delta":
		for i := range t.Messages {
			if t.Messages[i].ItemID == key {
				t.Messages[i].Content += d.Delta
				return
			}
		}
		t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "assistant", Kind: "agentMessage", ItemID: key, Content: d.Delta, SentAtMs: now, StartedAtMs: now})
	case "thinking_start", "thinking_delta":
		key += ":thinking"
		for i := range t.Messages {
			if t.Messages[i].ItemID == key {
				t.Messages[i].Content += d.Delta
				return
			}
		}
		c.Upsert(t, model.Message{Role: "activity", Kind: "reasoning", ItemID: key, Content: d.Delta, Status: "inProgress", SentAtMs: now, StartedAtMs: now})
	case "thinking_end":
		key += ":thinking"
		for i := range t.Messages {
			if t.Messages[i].ItemID == key {
				t.Messages[i].Status, t.Messages[i].CompletedAtMs = "completed", now
			}
		}
	}
}

// assistant settles a finished assistant message: its text and thinking as
// pi recorded them, and an error when the request failed.
func (b Adapter) assistant(t *model.Thread, m pinative.Message, now int64) {
	c := b.host
	if m.Model != "" {
		t.LaunchModel = m.Model
	}
	for i, block := range m.Blocks() {
		key := fmt.Sprintf("%s:%d", t.ClaudeMessageID, i)
		switch block.Type {
		case "text":
			if block.Text != "" {
				c.Upsert(t, model.Message{Role: "assistant", Kind: "agentMessage", ItemID: key, Content: block.Text, SentAtMs: now, StartedAtMs: now})
			}
		case "thinking":
			if block.Thinking != "" {
				c.Upsert(t, model.Message{Role: "activity", Kind: "reasoning", ItemID: key + ":thinking", Content: block.Thinking, Status: "completed", SentAtMs: now, StartedAtMs: now, CompletedAtMs: now})
			}
		}
	}
	if m.StopReason == "error" {
		msg := textutil.FirstNonEmpty(m.ErrorMessage, tr("Pi request failed"))
		t.Status = msg
		t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: msg, StartedAtMs: now})
	}
}

// endTurn finishes a turn; the process stays for the next.
func (b Adapter) endTurn(id int, messages []pinative.Message) {
	c := b.host
	state := dg.Completed
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		switch messages[i].StopReason {
		case "error":
			state = dg.Failed
		case "aborted":
			state = dg.Interrupted
		}
		break
	}
	if c.ThreadWithID(id).StopRequested {
		state = dg.Interrupted
	}
	c.Update(id, func(t *model.Thread) {
		settle(t, state)
		pinative.SaveRecord(*t, "")
	})
	if state == dg.Completed {
		c.Memories().CompletedTurn(id, "")
	}
	c.ManagedResult(id, state, "")
	c.DrainQueue(id)
}

// settle ends a turn's state and its unfinished items.
func settle(t *model.Thread, state dg.State) {
	t.Busy, t.TaskStopping = false, false
	t.CompletedAtMs = time.Now().UnixMilli()
	item := "completed"
	switch state {
	case dg.Interrupted:
		item = "interrupted"
		t.Status = ""
		if t.StopRequested {
			t.Status = model.StatusStopped
		}
	case dg.Failed:
		item = "failed"
	default:
		t.Status = ""
	}
	for i := range t.Messages {
		if t.Messages[i].Status == "inProgress" {
			t.Messages[i].Status = item
			t.Messages[i].CompletedAtMs = t.CompletedAtMs
		}
	}
}

// Finish handles the end of a conversation's pi process.
func (b Adapter) Finish(id int, run *pinative.Client, err error) {
	c := b.host
	c.SetRun(id, nil)
	t := c.ThreadWithID(id)
	if t.ID == 0 {
		return
	}
	busy := t.Busy
	state := dg.Interrupted
	if err != nil && !t.StopRequested && !c.Cancelled(err) {
		state = dg.Failed
	}
	c.Update(id, func(t *model.Thread) {
		if err != nil && !t.StopRequested {
			t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "error", Content: err.Error(), StartedAtMs: time.Now().UnixMilli()})
		}
		if busy {
			settle(t, state)
		}
		if err != nil && !t.StopRequested {
			t.Status = err.Error()
		}
		pinative.SaveRecord(*t, "")
	})
	if busy && !c.FinishPausedDelivery(t.ExecutionID, err) {
		c.ManagedResult(id, state, "")
	}
}

// LoadHistory reads Miya's copy of the conversation, or pi's session file
// when that copy has lost its messages.
func (b Adapter) LoadHistory(t model.Thread) {
	c := b.host
	if t.HistoryLoaded || t.Loading {
		return
	}
	cwd := c.ThreadWorkspace(t.ID)
	c.Update(t.ID, func(t *model.Thread) { t.Loading = true })
	go func() {
		messages, err := History(t.ServerID)
		c.Dispatch(func() {
			if c.ThreadWorkspace(t.ID) != cwd || c.ThreadWithID(t.ID).ServerID != t.ServerID {
				return
			}
			c.Update(t.ID, func(t *model.Thread) {
				t.Loading, t.HistoryLoaded = false, true
				if err != nil {
					t.Status = "Local history is unavailable. Send a message to continue this conversation."
					return
				}
				t.Messages = messages
				for i := range t.Messages {
					t.Messages[i].ID = c.NextID()
					if t.Messages[i].Status == "inProgress" {
						t.Messages[i].Status = "interrupted"
					}
				}
				t.Resumed, t.Loaded = true, true
			})
			c.DrainQueue(t.ID)
		})
	}()
}

// ToolCall names a pi tool call as Miya presents Claude Code's, so the
// conversation shows what ran, what was read or edited.
func ToolCall(name string, args json.RawMessage) (string, string) {
	var input map[string]any
	_ = json.Unmarshal(args, &input)
	if input == nil {
		input = map[string]any{}
	}
	shown := name
	switch name {
	case "bash":
		shown = "Bash"
	case "read":
		shown = "Read"
	case "edit":
		shown = "Edit"
	case "write":
		shown = "Write"
	case "grep":
		shown = "Grep"
	case "find":
		shown = "Glob"
	case "ls":
		shown = "LS"
	}
	if p, ok := input["path"].(string); ok && (name == "read" || name == "edit" || name == "write") {
		input["file_path"] = p
	}
	data, err := json.Marshal(input)
	if err != nil {
		return shown, string(args)
	}
	return shown, string(data)
}
