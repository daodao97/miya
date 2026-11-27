package presentation

import (
	"encoding/json"
	"regexp"
	"strings"

	"miya/internal/chat/model"
	chattext "miya/internal/chat/text"
	dg "miya/internal/delegation"
	"miya/internal/textutil"
)

// IsDelegationCall recognizes only start_task, the call that creates a task to
// follow. send_thread messages an existing conversation without one, and
// polling or reading a task stays in the surrounding activity run.
func IsDelegationCall(m model.Message) bool { return miyaTool(m) == "start_task" }

// miyaTool names a Miya MCP call without decoding its arguments; it runs for
// every tool row on every frame.
func miyaTool(m model.Message) string {
	if m.Role != "tool" {
		return ""
	}
	switch strings.ToLower(m.Kind) {
	case "mcptoolcall":
		if m.Item != nil && m.Item.Server == "miya" {
			return m.Item.Tool
		}
	case "claudetool":
		name, _ := chattext.ToolCall(m)
		if tool, ok := strings.CutPrefix(name, "mcp__miya__"); ok {
			return tool
		}
	}
	return ""
}

func DelegationCall(m model.Message) (requestID string, card model.DelegationCard, ok bool) {
	if !IsDelegationCall(m) {
		return "", card, false
	}
	call, _ := ToolCall(m)
	var args struct {
		RequestID string `json:"request_id"`
		Title     string `json:"title"`
		Task      string `json:"task"`
		Agent     string `json:"agent"`
		Bot       string `json:"bot"`
		Thread    string `json:"thread_id"`
	}
	_ = json.Unmarshal(call.Arguments, &args)
	card.Title = textutil.ClipRunes(textutil.FirstNonEmpty(args.Title, textutil.FirstLine(args.Task)), 120)
	card.Request = textutil.ClipRunes(strings.TrimSpace(args.Task), 1600)
	card.Agent = textutil.ClipRunes(args.Bot, 80)
	if card.Agent == "" {
		if ref, valid := model.ParseThreadURL(args.Thread); valid {
			card.Agent = model.AgentName(ref.Backend)
		} else if agent := model.NormalizeAgentID(args.Agent); agent != "" {
			card.Agent = model.AgentName(agent)
		}
	}
	switch m.Status {
	case "inProgress", "in_progress", "running":
		card.State = string(dg.Starting)
	case "failed", "interrupted":
		card.State = m.Status
		// Keep rejection details available when Miya did not create a task.
		card.Detail = failureText(m.Content)
	}
	return args.RequestID, card, true
}

// DelegationRead returns the task a completed read_task call fetched.
func DelegationRead(m model.Message) (string, bool) {
	if miyaTool(m) != "read_task" || m.Status != "completed" {
		return "", false
	}
	call, _ := ToolCall(m)
	var args struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(call.Arguments, &args) != nil || args.TaskID == "" {
		return "", false
	}
	return args.TaskID, true
}

// FoldedDelegationRead is a read_task call the timeline merged into the
// receipt it followed.
func FoldedDelegationRead(m model.Message) bool {
	return m.Role == "tool" && len(m.Delegations) == 1 && m.Delegations[0].Read
}

// failureText prefers the message of a structured tool error over its JSON.
func failureText(content string) string {
	content = strings.TrimSpace(content)
	var payload struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(content), &payload) == nil {
		var text string
		var nested struct {
			Message string `json:"message"`
		}
		switch {
		case payload.Message != "":
			content = payload.Message
		case json.Unmarshal(payload.Error, &text) == nil && text != "":
			content = text
		case json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "":
			content = nested.Message
		}
	}
	return textutil.ClipRunes(strings.TrimSpace(content), 1600)
}

var delegationNoticeItem = regexp.MustCompile(`(?m)^- ([\s\S]*?): (completed|failed|interrupted) \(task_id: (task-[0-9a-f]{32}), session: ([^\n]*)\)$`)

// DelegationReceipts reads the task references from native continuation prompts.
func DelegationReceipts(m model.Message) []model.DelegationCard {
	if m.Role != "user" || !strings.HasPrefix(m.Content, model.DelegationNoticeHeading+" ") {
		return nil
	}
	var receipts []model.DelegationCard
	for _, match := range delegationNoticeItem.FindAllStringSubmatch(m.Content, -1) {
		card := model.DelegationCard{TaskID: match[3], Title: textutil.ClipRunes(match[1], 120), State: match[2]}
		if ref, ok := model.ParseThreadURL(match[4]); ok {
			card.Thread, card.Agent = ref.URL(), model.AgentName(ref.Backend)
		}
		receipts = append(receipts, card)
	}
	return receipts
}

// DelegationResults falls back to the native receipt while working offline
// with history recorded before the service supplied the result preview.
func DelegationResults(m model.Message) []model.DelegationCard {
	if len(m.Delegations) > 0 {
		return m.Delegations
	}
	return DelegationReceipts(m)
}
