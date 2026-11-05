package main

import (
	"miya/internal/model"
	"strings"
)

func agentReferenceText(agent, title string) string {
	uri := model.AgentURL(agent)
	if uri == "" {
		return ""
	}
	if title == "" {
		title = backendFor(agent).Name()
	}
	title = strings.Join(strings.Fields(title), " ")
	title = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(title)
	return "#[" + title + "](" + uri + ")"
}

func agentChoices(query string, allowed []string) []composerChoice {
	query = strings.ToLower(query)
	var out []composerChoice
	for _, b := range agentBackends {
		if allowed != nil {
			found := false
			for _, id := range allowed {
				if b.ID() == id {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		aliases := b.ID() + " " + b.Name()
		if b.ID() == agentCodex {
			aliases += " openai"
		}
		if b.ID() == agentClaude {
			aliases += " claude anthropic"
		}
		if !strings.Contains(strings.ToLower(aliases), query) {
			continue
		}
		out = append(out, composerChoice{Name: b.Name(), Agent: b.ID(), Detail: tr("Run a task in an independent conversation")})
	}
	return out
}

func insertAgentReference(value string, token composerToken, agent, title string) string {
	if token.Kind != '#' && token.Kind != '@' || token.Start < 0 || token.End > len(value) || token.End < token.Start {
		return value
	}
	if tag := agentReferenceText(agent, title); tag != "" {
		return value[:token.Start] + tag + " " + value[token.End:]
	}
	return value
}

// Instructions are added only to native input. The user's visible message,
// structured parts, draft and memory evidence retain exactly their own text.
func collaborationTaggedInput(text string, parts []MessagePart) string {
	var mentions []model.AgentMention
	for _, part := range parts {
		if part.Type == "agent_ref" && registeredBackend(part.Agent) {
			mentions = append(mentions, model.AgentMention{Agent: part.Agent})
		}
	}
	if len(mentions) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n<miya_collaboration_v1>\nAgent tags identify the user's requested collaboration targets by canonical backend ID. Use Miya start_task to delegate the relevant work in independent sessions, then read_task/wait_task to collect fixed results and continue the user's task. Selecting a tag did not start a task. Use a unique request_id per operation and preserve it on retry. Default to context none and notify on_finish so completion resumes you; use notify none only for explicitly record-only work. While actively waiting, poll wait_task; do not leave delegated work untracked. @conversation references remain context only. Targets:\n")
	seen := map[string]bool{}
	for _, m := range mentions {
		if !seen[m.Agent] {
			b.WriteString("- " + m.Agent + "\n")
			seen[m.Agent] = true
		}
	}
	b.WriteString("</miya_collaboration_v1>")
	return b.String()
}
