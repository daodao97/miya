package notification

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"miya/internal/agent/codex/native"
	"miya/internal/model"
	"miya/internal/textutil"
)

const noticeTextLimit = 160

var noticeMarkdownLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^\n]*?\)`)

// Notifications quote the agent's own answer, without another model request.
func noticeSummary(text string) string {
	// Structured replies sometimes contain a dedicated summary field.
	var object map[string]json.RawMessage
	structured := strings.TrimSpace(text)
	if strings.HasPrefix(structured, "```json\n") {
		structured = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(structured, "```json\n"), "```"))
	}
	if json.Unmarshal([]byte(structured), &object) == nil {
		if raw, ok := object["summary"]; ok {
			text = ""
			_ = json.Unmarshal(raw, &text)
		}
	}
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fence = line[:3]
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimLeft(line, "-*•> ")
		line = noticeMarkdownLink.ReplaceAllString(line, "$1")
		line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
		line = noticePlainText(line)
		for i, r := range line {
			if strings.ContainsRune("。！？", r) || r == '.' && (i+1 == len(line) || line[i+1] == ' ') {
				return line[:i+len(string(r))]
			}
		}
		if line != "" {
			return line
		}
	}
	return ""
}

func noticePlainText(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > noticeTextLimit {
		return string(runes[:noticeTextLimit-1]) + "…"
	}
	return text
}

func CompletionBody(old, next model.Thread) string {
	if next.CompletionNoticeBody != "" {
		return next.CompletionNoticeBody
	}
	// Codex clears TurnID on completion. The busy snapshot still owns it.
	turn := textutil.FirstNonEmpty(old.TurnID, next.TurnID)
	for i := len(next.Messages) - 1; i >= 0; i-- {
		m := next.Messages[i]
		if m.Role == "user" {
			break
		}
		if turn != "" {
			if m.TurnID != turn {
				continue
			}
		} else if next.StartedAtMs == 0 || m.StartedAtMs < next.StartedAtMs {
			// Claude has no server turn IDs, including autonomous loop turns.
			continue
		}
		if next.Status != "" {
			if m.Role == "error" {
				if text := noticeSummary(m.Content); text != "" {
					return text
				}
			}
			continue
		}
		if m.Role == "assistant" && (m.Item == nil || m.Item.Phase == "" || m.Item.Phase == "final_answer") {
			if text := noticeSummary(m.Content); text != "" {
				return text
			}
			// Do not substitute earlier progress for an empty final answer.
			break
		}
	}
	if next.Status != "" {
		if text := noticeSummary(next.Status); text != "" {
			return text
		}
	}
	if task := Task(next); task != "" {
		return task
	}
	return tr("Task completed. Open the conversation to view the result.")
}

// Compute before messages are stripped from service snapshots. Windows can
// notify about an unsubscribed conversation without loading its transcript.
func UpdateCompletionBody(old model.Thread, next *model.Thread) {
	if next.Busy {
		next.CompletionNoticeBody = ""
	} else if old.Busy && next.CompletedAtMs != 0 && next.Status != tr("Stopped") {
		next.CompletionNoticeBody = ""
		next.CompletionNoticeBody = CompletionBody(old, *next)
	}
}

func Task(t model.Thread) string {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := t.Messages[i]
		if m.Role != "user" {
			continue
		}
		text := noticePlainText(model.CleanUserText(m.Content))
		switch strings.ToLower(strings.Trim(text, " 。.!！?？")) {
		case "", "继续", "继续吧", "实现吧", "干吧", "好的", "好", "可以", "嗯", "ok", "yes", "continue", "go ahead":
			continue
		}
		return noticePlainText(tr("Task: ") + text)
	}
	title := noticePlainText(t.Title)
	switch strings.ToLower(title) {
	case "", "codex", "claude", "claude code", "claude code 对话", "new conversation", "新对话":
		return ""
	}
	if strings.HasPrefix(title, "/") || strings.HasPrefix(title, "~/") || len(title) >= 3 && title[1] == ':' && (title[2] == '\\' || title[2] == '/') {
		return ""
	}
	return noticePlainText(tr("Task: ") + title)
}

func Approval(t model.Thread, a codex.Approval) Notice {
	n := Notice{Thread: strconv.Itoa(t.ID), Title: tr("Miya · Confirmation needed")}
	switch a.Method {
	case "item/tool/requestUserInput":
		n.Title = tr("Miya · Answer needed")
		for _, q := range a.Questions {
			if n.Body = noticePlainText(q.Question); n.Body != "" {
				break
			}
		}
		if n.Body == "" {
			n.Body = tr("Answer a question to continue the task.")
		}
	case "mcpServer/elicitation/request":
		n.Body = noticePlainText(a.Message)
	case "claude/permission":
		// Claude's Reason is raw tool input, not a user-facing explanation.
		var input struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(a.Payload, &input)
		if input.Command != "" {
			n.Body = noticePlainText(tr("Allow execution of ") + input.Command + "?")
		} else if a.Command != "" {
			n.Body = noticePlainText(tr("Permission is required to use ") + a.Command + tr(" to continue the task."))
		}
	default:
		n.Body = noticePlainText(a.Reason)
		if n.Body == "" && a.Command != "" {
			n.Body = noticePlainText(tr("Allow execution of ") + a.Command + "?")
		}
		if n.Body == "" && a.Method == "item/fileChange/requestApproval" {
			n.Body = tr("Confirm the file changes to continue the task.")
		}
		if n.Body == "" && a.Method == "item/permissions/requestApproval" {
			n.Body = tr("Confirm operation permissions to continue the task.")
		}
	}
	if n.Body == "" {
		n.Body = Task(t)
	}
	if n.Body == "" {
		n.Body = tr("The task is waiting for your confirmation.")
	}
	return n
}
