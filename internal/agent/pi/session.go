package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/textutil"
)

// History is a pi conversation as Miya shows it: Miya's record, or, when
// the record has lost its messages, the session file pi keeps.
func History(id string) ([]model.Message, error) {
	rec, err := pinative.LoadRecord(id)
	if err != nil {
		return nil, err
	}
	if len(rec.Thread.Messages) > 0 || rec.SessionFile == "" {
		return rec.Thread.Messages, nil
	}
	return SessionMessages(rec.SessionFile)
}

type sessionEntry struct {
	Type     string           `json:"type"`
	ID       string           `json:"id"`
	ParentID *string          `json:"parentId"`
	Message  pinative.Message `json:"message"`
}

// SessionMessages reads the active branch of a pi session file: from its
// last entry back through the entries it continues.
func SessionMessages(path string) ([]model.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	byID := map[string]sessionEntry{}
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var e sessionEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.ID == "" || e.Type == "session" {
			continue
		}
		byID[e.ID] = e
		last = e.ID
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var branch []sessionEntry
	for id, seen := last, map[string]bool{}; id != "" && !seen[id]; {
		e, ok := byID[id]
		if !ok {
			break
		}
		seen[id] = true
		branch = append(branch, e)
		if e.ParentID == nil {
			break
		}
		id = *e.ParentID
	}
	var out []model.Message
	tools := map[string]int{}
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type != "message" {
			continue
		}
		m := branch[i].Message
		at := m.Timestamp
		switch m.Role {
		case "user":
			if text := memory.StripLegacyContact(memory.StripContext(pinative.Text(m.Blocks()))); text != "" {
				out = append(out, model.Message{Role: "user", Content: text, SentAtMs: at, Parts: model.MessageParts(text)})
			}
		case "assistant":
			for _, b := range m.Blocks() {
				switch b.Type {
				case "text":
					if b.Text != "" {
						out = append(out, model.Message{Role: "assistant", Kind: "agentMessage", Content: b.Text, Status: "completed", SentAtMs: at, StartedAtMs: at, CompletedAtMs: at})
					}
				case "thinking":
					if b.Thinking != "" {
						out = append(out, model.Message{Role: "activity", Kind: "reasoning", Content: b.Thinking, Status: "completed", SentAtMs: at, StartedAtMs: at, CompletedAtMs: at})
					}
				case "toolCall":
					name, input := ToolCall(b.Name, b.Arguments)
					tools[b.ID] = len(out)
					out = append(out, model.Message{Role: "tool", Kind: "claudeTool", ItemID: b.ID, Content: name + "\n" + input, Status: "completed", SentAtMs: at, StartedAtMs: at, CompletedAtMs: at})
				}
			}
			if m.StopReason == "error" {
				out = append(out, model.Message{Role: "error", Content: textutil.FirstNonEmpty(m.ErrorMessage, tr("Pi request failed")), StartedAtMs: at})
			}
		case "toolResult":
			i, ok := tools[m.ToolCallID]
			if !ok {
				continue
			}
			if m.IsError {
				out[i].Status = "failed"
			}
			if at > 0 {
				out[i].CompletedAtMs = at
			}
			if text := strings.TrimSpace(pinative.Text(m.Blocks())); text != "" {
				out[i].Content += "\n" + textutil.Bounded(text)
			}
		}
	}
	return out, nil
}
