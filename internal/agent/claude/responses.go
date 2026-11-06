package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"miya/internal/interaction"
)

// The compatibility Host accepts the established permission/answer envelope.
// Its translation to stream-json is owned by RespondLegacy in this adapter.
func encodeLegacyResponse(reply interaction.Response) any {
	if reply.Action == interaction.Submit || reply.Action == interaction.Skip {
		answers := map[string]any{}
		if reply.Action != interaction.Skip {
			for id, text := range reply.Answers {
				answers[id] = map[string]any{"answers": []string{text}}
			}
		}
		return map[string]any{"answers": answers}
	}
	decision := "decline"
	if reply.Action == interaction.Allow {
		decision = "accept"
	}
	return map[string]string{"decision": decision}
}

func (b Adapter) RespondLegacy(a interaction.Request, result any) bool {
	c := b.host
	var id string
	_ = json.Unmarshal(a.ID, &id)
	if !strings.HasPrefix(id, "claude:") {
		return false
	}
	var thread int
	_, _ = fmt.Sscanf(id, "claude:%d:", &thread)
	parts := strings.SplitN(id, ":", 3)
	c.RemoveRequest(a.ID)
	run := c.Run(thread)
	if run == nil || len(parts) != 3 {
		return true
	}
	encoded, _ := json.Marshal(result)
	var r struct {
		Decision string `json:"decision"`
		Answers  map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	_ = json.Unmarshal(encoded, &r)
	response := map[string]any{"behavior": "deny", "message": "The user denied this operation"}
	if r.Decision == "accept" || r.Answers != nil {
		var input map[string]any
		_ = json.Unmarshal(a.Payload, &input)
		if input == nil {
			input = map[string]any{}
		}
		if r.Answers != nil {
			answers := map[string]string{}
			for k, v := range r.Answers {
				answers[k] = strings.Join(v.Answers, ", ")
			}
			input["answers"] = answers
		}
		response = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	go func() { _ = run.Reply(parts[2], response) }()
	return true
}
