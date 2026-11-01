package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"miya/internal/codex"
)

var computerUseAccessPrompt = regexp.MustCompile(`^Allow Computer Use to use "([^"\r\n]+)"\?$`)

// Only the known application-access prompt supports a session grant. General
// MCP forms and browser authentication still require a separate response.
func rememberableMCPAccess(a codex.Approval) bool {
	if a.Method != "mcpServer/elicitation/request" || a.ServerName != "cua_repl" || a.ThreadID == "" || (a.Mode != "" && a.Mode != "form") || !computerUseAccessPrompt.MatchString(a.Message) {
		return false
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(a.RequestedSchema, &schema) != nil || len(schema.Properties) != 0 {
		return false
	}
	return validateForm(a.RequestedSchema, map[string]any{}) == nil
}

func mcpSessionPermissionKey(a codex.Approval) string {
	var schema any
	_ = json.Unmarshal(a.RequestedSchema, &schema)
	canonical, _ := json.Marshal(schema)
	data, _ := json.Marshal([]string{a.ThreadID, a.ServerName, a.Message, string(canonical)})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func (c *chatController) rememberedMCPAccess(a codex.Approval) bool {
	return rememberableMCPAccess(a) && c.mcpSessionAccess[mcpSessionPermissionKey(a)]
}
func mcpAccessAccepted() map[string]any {
	return map[string]any{"action": "accept", "content": map[string]any{}}
}

func (c *chatController) respondMCPAlways(a codex.Approval) error {
	if c.remote != nil {
		if err := c.remote.call("respond", svcRespond{ID: a.ID, Remember: true}, nil); err != nil {
			return err
		}
		c.removeRequest(a.ID)
		return nil
	}
	// Resolve from the live request queue; stale cards cannot create a grant.
	for _, pending := range c.approvals() {
		if !sameJSON(pending.ID, a.ID) {
			continue
		}
		if !rememberableMCPAccess(pending) {
			return errors.New("此请求不支持对话内始终允许")
		}
		if c.mcpSessionAccess == nil {
			c.mcpSessionAccess = map[string]bool{}
		}
		// Deliberately memory-only: scoped to this thread and backend lifetime.
		c.mcpSessionAccess[mcpSessionPermissionKey(pending)] = true
		c.respond(pending, mcpAccessAccepted())
		return nil
	}
	return errors.New("请求已处理或已过期")
}
