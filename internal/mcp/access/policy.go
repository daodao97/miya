package access

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"

	"miya/internal/interaction"
)

var ComputerUseAccessPrompt = regexp.MustCompile(`^Allow Computer Use to use "([^"\r\n]+)"\?$`)

// MiyaToolPrompt is how Codex asks before a tool of the miya MCP server runs.
var MiyaToolPrompt = regexp.MustCompile(`^Allow the miya MCP server to run tool "([^"\r\n]+)"\?$`)

// MiyaTool is the miya tool a, a Codex form, asks to run, when it is that
// question.
func MiyaTool(a interaction.Request) (string, bool) {
	if a.Method != "mcpServer/elicitation/request" || a.ServerName != "miya" || (a.Mode != "" && a.Mode != "form") {
		return "", false
	}
	m := MiyaToolPrompt.FindStringSubmatch(a.Message)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func Accepted() map[string]any {
	return map[string]any{"action": "accept", "content": map[string]any{}}
}

func Key(a interaction.Request) string {
	var schema any
	_ = json.Unmarshal(a.RequestedSchema, &schema)
	canonical, _ := json.Marshal(schema)
	data, _ := json.Marshal([]string{a.ThreadID, a.ServerName, a.Message, string(canonical)})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Only the known application-access prompt supports a session grant. General
// MCP forms and browser authentication still require a separate response.
func Rememberable(a interaction.Request) bool {
	if a.Method != "mcpServer/elicitation/request" || a.ServerName != "cua_repl" || a.ThreadID == "" || (a.Mode != "" && a.Mode != "form") || !ComputerUseAccessPrompt.MatchString(a.Message) {
		return false
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(a.RequestedSchema, &schema) != nil || len(schema.Properties) != 0 {
		return false
	}
	return interaction.ValidateForm(a.RequestedSchema, map[string]any{}) == nil
}
