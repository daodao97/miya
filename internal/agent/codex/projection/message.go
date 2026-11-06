package projection

import (
	"encoding/json"
	"fmt"
	"strings"

	codex "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	chattext "miya/internal/chat/text"
	"miya/internal/memory"
	"miya/internal/textutil"
	"miya/locales"
)

func ItemMessage(item codex.Item) (model.Message, bool) {
	m := model.Message{Kind: item.Type, Status: item.Status, Item: Activity(item)}
	switch strings.ToLower(item.Type) {
	case "agentmessage":
		m.Role = "assistant"
		m.Content = item.Text
	case "usermessage":
		m.Role = "user"
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Path     string `json:"path"`
			URL      string `json:"url"`
			ImageURL string `json:"image_url"`
			Name     string `json:"name"`
			Data     string `json:"data"`
			MIMEType string `json:"mime_type"`
		}
		_ = json.Unmarshal(item.Content, &parts)
		for _, v := range parts {
			switch v.Type {
			case "text":
				v.Text = memory.StripContext(v.Text)
				for _, uri := range msgfmt.AttachmentImages(v.Text) {
					m.Images = append(m.Images, uri)
				}
				m.Content += model.CleanUserText(v.Text)
			case "image", "input_image":
				src := strings.TrimSpace(v.URL)
				if src == "" {
					src = strings.TrimSpace(v.ImageURL)
				}
				if src == "" {
					src = strings.TrimSpace(v.Data)
					if src != "" && !strings.HasPrefix(src, "data:") {
						mimeType := v.MIMEType
						if mimeType == "" {
							mimeType = "image/png"
						}
						src = "data:" + mimeType + ";base64," + src
					}
				}
				if src != "" {
					if source := msgfmt.ImageSource(src); source != "" {
						m.Images = append(m.Images, source)
					}
				}
			case "localImage", "local_image":
				if uri := chattext.LocalImageURI(v.Path); uri != "" {
					m.Images = append(m.Images, uri)
				} else if strings.Contains(v.Path, "/") || strings.Contains(v.Path, "\\") {
					m.Content += tr("\nImage: ") + v.Path
				}
			case "skill", "mention":
				m.Content += "\n[" + v.Type + ": " + v.Name + " " + v.Path + "]"
			default:
				m.Content += "\n[" + v.Type + "]"
			}
		}
	case "reasoning":
		m.Role = "activity"
		m.Content = strings.Join(item.Summary, "\n")
		if m.Content == "" {
			m.Content = tr("Thinking…")
		}
	case "plan":
		m.Role = "activity"
		m.Content = item.Text
	case "commandexecution":
		m.Role = "tool"
		m.Content = item.Status + " · " + item.Command
		if item.ExitCode != nil {
			m.Content += fmt.Sprintf(tr("\nExit code: %d"), *item.ExitCode)
		}
		if item.AggregatedOutput != "" {
			m.Content += "\n" + textutil.Bounded(item.AggregatedOutput)
		}
	case "filechange":
		m.Role = "tool"
		m.Content = tr("File changes · ") + item.Status
		for _, change := range item.Changes {
			m.Content += "\n" + change.Path + "\n" + textutil.Bounded(change.Diff)
		}
	case "mcptoolcall", "dynamictoolcall":
		m.Role = "tool"
		m.Content = item.Status + " · " + strings.Trim(item.Server+" / "+item.Tool, " /")
		for _, raw := range []json.RawMessage{item.Arguments, item.Result, item.ToolError} {
			if v := msgfmt.JsonText(raw); v != "" {
				m.Content += "\n" + v
			}
		}
	case "collabagenttoolcall":
		m.Role = "tool"
		m.Agents = FromCodex(item)
		if len(m.Agents) > 0 {
			// The agents list shows their states; what was sent to them stays.
			m.Content = CodexCollabContent(item, m.Agents)
		} else {
			m.Content = item.Status + " · " + item.Tool + "\n" + msgfmt.JsonText(item.AgentsStates)
		}
	case "subagentactivity":
		m.Role = "activity"
		m.Content = CodexActivity(item)
	case "websearch":
		m.Role = "tool"
		m.Content = tr("Web search · ") + item.Query + "\n" + msgfmt.JsonText(item.Result)
	case "imageview":
		m.Role = "tool"
		m.Content = tr("Viewed image\n") + item.Path
	case "imagegeneration":
		m.Role = "tool"
		m.Content = tr("Image generation · ") + item.Status + "\n" + item.SavedPath + "\n" + msgfmt.JsonText(item.Failure)
	case "enteredreviewmode", "exitedreviewmode":
		m.Role = "activity"
		m.Content = item.Type + "\n" + item.Review
	case "contextcompaction":
		m.Role = "activity"
		m.Content = tr("Conversation context compacted")
	case "sleep":
		m.Role = "activity"
		m.Content = fmt.Sprintf(tr("Waiting · %d ms"), item.DurationMS)
	case "functioncalloutput":
		m.Role = "tool"
		m.Content = msgfmt.JsonText(item.Output)
	case "hookprompt":
		m.Role = "activity"
		m.Content = tr("Hook prompt")
	default:
		if item.Type == "" {
			return m, false
		}
		m.Role = "activity"
		m.Content = tr("Codex event: ") + item.Type
	}
	// Attachment placeholders can be introduced by more than one content item;
	// sanitize after the complete message has been assembled as the final guard.
	if m.Role == "user" {
		m.Content = model.CleanUserText(m.Content)
	}
	return m, true
}

func CodexTurnTimes(turn codex.Turn) (start, end int64) {
	if turn.StartedAt > 0 {
		start = turn.StartedAt * 1000
	}
	if turn.Status == "inProgress" {
		return start, 0
	}
	if turn.CompletedAt > 0 {
		end = turn.CompletedAt * 1000
	}
	if turn.DurationMS != nil && *turn.DurationMS >= 0 {
		if start > 0 {
			end = start + *turn.DurationMS
		} else if end > *turn.DurationMS {
			start = end - *turn.DurationMS
		}
	}
	return start, end
}

// Claude's transport does not attach a turn ID to its response items.

func RecordCodexOutput(t *model.Thread, usage *codex.TokenUsage) {
	if !t.Busy || usage == nil || usage.Total.OutputTokens == nil {
		return
	}
	total := *usage.Total.OutputTokens
	if t.TurnOutputBaseline == nil {
		// A resumed session may first report its historical total during this
		// turn. The last request identifies which output is new.
		last := usage.Last.OutputTokens
		if last == nil || *last < 0 || *last > total {
			return
		}
		t.TurnOutputBaseline = new(total - *last)
	}
	if total < *t.TurnOutputBaseline {
		// A reset/compacted total cannot be compared with the old baseline.
		t.TurnOutputTokens = nil
		return
	}
	t.TurnOutputTokens = new(total - *t.TurnOutputBaseline)
}

func HistoryMessages(remote codex.Thread) []model.Message {
	var out []model.Message
	for _, turn := range remote.Turns {
		start, end := CodexTurnTimes(turn)
		for _, item := range turn.Items {
			if m, ok := ItemMessage(item); ok {
				if m.Status == "" {
					m.Status = turn.Status
				}
				m.ItemID = item.ID
				if m.Role == "user" {
					own, _ := model.SplitReferenceContext(m.Content)
					m.Parts = model.MessageParts(own)
				}
				m.TurnID = turn.ID
				m.SentAtMs, m.StartedAtMs, m.CompletedAtMs = start, start, end
				out = append(out, m)
			}
		}
		if turn.Error != nil {
			out = append(out, model.Message{Role: "error", Kind: "error", Status: "failed", Content: turn.Error.Message, TurnID: turn.ID, ItemID: "turn-error", SentAtMs: start, StartedAtMs: start, CompletedAtMs: end})
		}
	}
	return out
}

func ApplyGoal(t *model.Thread, goal *codex.Goal) {
	if goal == nil {
		for i, task := range t.Tasks {
			if task.Kind == "goal" {
				t.Tasks = append(t.Tasks[:i:i], t.Tasks[i+1:]...)
				return
			}
		}
		return
	}
	model.SetThreadTask(t, model.ThreadTask{ID: "goal", Kind: "goal", Title: goal.Objective, Status: goal.Status,
		TokensUsed: goal.TokensUsed, TokenBudget: goal.TokenBudget, TimeUsedSeconds: goal.TimeUsedSeconds})
}

func tr(s string) string { return locales.T(s) }
