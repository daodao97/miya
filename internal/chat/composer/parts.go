package composer

import (
	"errors"
	"fmt"
	"strings"

	"miya/internal/chat/model"
)

func PartsText(parts []model.MessagePart) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "text":
			b.WriteString(p.Text)
		case "thread_ref":
			if p.Ref == nil || !p.Ref.Valid() {
				return "", errors.New("Invalid conversation reference identity")
			}
			b.WriteString(model.ReferenceText(*p.Ref, p.TitleSnapshot))
		case "agent_ref":
			tag := AgentReferenceText(p.Agent, p.TitleSnapshot)
			if tag == "" {
				return "", errors.New("Invalid agent reference identity")
			}
			b.WriteString(tag)
		default:
			return "", fmt.Errorf("Unknown message content type %q", p.Type)
		}
	}
	return b.String(), nil
}
