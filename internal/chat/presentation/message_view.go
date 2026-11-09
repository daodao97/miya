package presentation

import (
	"fmt"
	"strings"

	"miya/internal/chat/model"
	chattext "miya/internal/chat/text"
	"miya/internal/textutil"
	"miya/locales"
)

// Message is the adapter projection for stored and live messages. New
// integrations supply Presentation directly; legacy native shapes are handled
// here so neither desktop nor phone imports a provider implementation.
func Message(m model.Message) model.MessagePresentation {
	if m.Presentation != nil {
		out := *m.Presentation
		if strings.EqualFold(m.Kind, "reasoning") {
			out.Content = ReasoningContent(out.Content)
		}
		return out
	}
	out := model.MessagePresentation{Content: m.Content, Title: legacyActivityTitle(m), Icon: ActivityIcon(m), Format: "text"}
	if m.Role == "user" {
		out.Content = chattext.CommandText(m.Content)
	}
	switch strings.ToLower(m.Kind) {
	case "reasoning":
		out.Content = ReasoningContent(out.Content)
		out.Format = "markdown"
	case "plan":
		out.Format = "markdown"
	case "diff":
		out.Format = "diff"
	case "filechange":
		if m.Item != nil {
			out.Format = "files"
			for _, change := range m.Item.Changes {
				out.Changes = append(out.Changes, model.PresentedFileChange{Path: change.Path, Diff: change.Diff})
			}
		}
	}
	for _, step := range m.Plan {
		out.Plan = append(out.Plan, model.PresentedPlanStep{Step: step.Step, Status: step.Status})
	}
	return out
}

// ReasoningContent removes old status placeholders from summary bodies,
// including messages retained by older services and phone caches.
func ReasoningContent(content string) string {
	switch strings.TrimSpace(strings.ReplaceAll(content, "...", "…")) {
	case "", "Thinking…", locales.TextIn("zh-Hans", "Thinking…"):
		return ""
	default:
		return content
	}
}

func legacyActivityTitle(m model.Message) string {
	active := m.Status == "inProgress" || m.Status == "in_progress"
	switch strings.ToLower(m.Kind) {
	case "claudetool":
		return chattext.ToolTitle(m)
	case "handoff":
		if m.HandoffTitle != "" {
			return tr("Handed off to conversation \"") + m.HandoffTitle + "\""
		}
		return tr("Handed off to another conversation")
	case "reasoning":
		if active {
			return tr("Thinking…")
		}
		return tr("Thought")
	case "commandexecution":
		if m.Item != nil && len(m.Item.CommandActions) > 0 {
			a := m.Item.CommandActions[0]
			switch a.Type {
			case "read":
				return tr("Read ") + textutil.FirstNonEmpty(a.Name, a.Path, tr("Files"))
			case "search":
				return tr("Searched ") + textutil.FirstNonEmpty(a.Query, a.Path, tr("Files"))
			case "listFiles":
				return tr("Listed ") + textutil.FirstNonEmpty(a.Path, tr("Files"))
			}
		}
		verb := tr("Ran ")
		if active {
			verb = tr("Running ")
		} else if m.Status == "failed" {
			verb = tr("Failed · ")
		} else if m.Status == "interrupted" {
			verb = tr("Stopped · ")
		}
		if m.Item != nil && m.Item.Command != "" {
			return verb + strings.SplitN(m.Item.Command, "\n", 2)[0]
		}
		return verb + "command"
	case "filechange":
		verb := tr("Edited")
		if active {
			verb = tr("Editing")
		} else if m.Status == "failed" {
			verb = tr("Failed to edit")
		}
		if m.Item != nil {
			return fmt.Sprintf("%s %d files", verb, len(m.Item.Changes))
		}
		return verb + " files"
	case "plan":
		if len(m.Plan) > 0 {
			done := 0
			for _, p := range m.Plan {
				if p.Status == "completed" {
					done++
				}
			}
			return fmt.Sprintf(tr("Plan · %d / %d completed"), done, len(m.Plan))
		}
		return tr("Proposed plan")
	case "mcptoolcall", "dynamictoolcall":
		if m.Item != nil {
			name := strings.Trim(m.Item.Server+" · "+m.Item.Tool, " ·")
			if name != "" {
				if active {
					return tr("Calling ") + name
				}
				return tr("Called ") + name
			}
		}
	case "websearch":
		if m.Item != nil {
			if active {
				return tr("Searching · ") + m.Item.Query
			}
			return tr("Searched · ") + m.Item.Query
		}
	case "diff":
		return tr("Changes in this turn")
	}
	return chattext.MessageLabel(m.Kind)
}
