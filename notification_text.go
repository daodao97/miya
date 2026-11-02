package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"miya/internal/codex"
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

func completionNoticeBody(old, next Thread) string {
	if next.CompletionNoticeBody != "" {
		return next.CompletionNoticeBody
	}
	// Codex clears TurnID on completion. The busy snapshot still owns it.
	turn := firstNonEmpty(old.TurnID, next.TurnID)
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
	if task := noticeTask(next); task != "" {
		return task
	}
	return "任务已完成，打开会话查看结果。"
}

// Compute before messages are stripped from service snapshots. Windows can
// notify about an unsubscribed conversation without loading its transcript.
func updateCompletionNoticeBody(old Thread, next *Thread) {
	if next.Busy {
		next.CompletionNoticeBody = ""
	} else if old.Busy && next.CompletedAtMs != 0 && next.Status != "Stopped" {
		next.CompletionNoticeBody = ""
		next.CompletionNoticeBody = completionNoticeBody(old, *next)
	}
}

func noticeTask(t Thread) string {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := t.Messages[i]
		if m.Role != "user" {
			continue
		}
		text := noticePlainText(cleanUserText(m.Content))
		switch strings.ToLower(strings.Trim(text, " 。.!！?？")) {
		case "", "继续", "继续吧", "实现吧", "干吧", "好的", "好", "可以", "嗯", "ok", "yes", "continue", "go ahead":
			continue
		}
		return noticePlainText("任务：" + text)
	}
	title := noticePlainText(t.Title)
	switch strings.ToLower(title) {
	case "", "codex", "claude", "claude code", "claude code 对话", "new conversation", "新对话":
		return ""
	}
	if strings.HasPrefix(title, "/") || strings.HasPrefix(title, "~/") || len(title) >= 3 && title[1] == ':' && (title[2] == '\\' || title[2] == '/') {
		return ""
	}
	return noticePlainText("任务：" + title)
}

func approvalNotice(t Thread, a codex.Approval) taskNotice {
	n := taskNotice{Thread: strconv.Itoa(t.ID), Title: "Miya · 需要你的确认"}
	switch a.Method {
	case "item/tool/requestUserInput":
		n.Title = "Miya · 需要你的回答"
		for _, q := range a.Questions {
			if n.Body = noticePlainText(q.Question); n.Body != "" {
				break
			}
		}
		if n.Body == "" {
			n.Body = "需要你回答一个问题，回答后继续任务。"
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
			n.Body = noticePlainText("是否允许执行 " + input.Command + "？")
		} else if a.Command != "" {
			n.Body = noticePlainText("需要允许使用 " + a.Command + "，才能继续任务。")
		}
	default:
		n.Body = noticePlainText(a.Reason)
		if n.Body == "" && a.Command != "" {
			n.Body = noticePlainText("是否允许执行 " + a.Command + "？")
		}
		if n.Body == "" && a.Method == "item/fileChange/requestApproval" {
			n.Body = "需要你确认文件修改，授权后继续任务。"
		}
		if n.Body == "" && a.Method == "item/permissions/requestApproval" {
			n.Body = "需要你确认操作权限，授权后继续任务。"
		}
	}
	if n.Body == "" {
		n.Body = noticeTask(t)
	}
	if n.Body == "" {
		n.Body = "任务正在等待你的确认。"
	}
	return n
}
