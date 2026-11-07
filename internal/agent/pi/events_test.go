package pi

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/chat/model"
	chattext "miya/internal/chat/text"
)

func TestToolCallsShowAsClaudeTools(t *testing.T) {
	for _, tc := range []struct {
		name, args, want string
	}{
		{"bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"read", `{"path":"/tmp/a.go"}`, "/tmp/a.go"},
		{"edit", `{"path":"/tmp/b.go","oldText":"x","newText":"y"}`, "/tmp/b.go"},
		{"grep", `{"pattern":"TODO"}`, "TODO"},
		{"custom_tool", `{"query":"q"}`, "custom_tool · q"},
	} {
		name, input := ToolCall(tc.name, json.RawMessage(tc.args))
		title := chattext.ToolTitle(model.Message{Kind: "claudeTool", Status: "completed", Content: name + "\n" + input})
		if !strings.Contains(title, tc.want) {
			t.Errorf("%s: title %q lacks %q", tc.name, title, tc.want)
		}
	}
}

func TestDefaultModelFollowsClaudeProvider(t *testing.T) {
	s := State{ClaudeModel: "claude-opus-5-5"}
	if defaultModel(s) != "" {
		t.Fatal("without a provider pi chooses its own model")
	}
	s.Provider.BaseURL, s.Provider.Token = "http://proxy", "t"
	if got := defaultModel(s); got != "claude-opus-5-5" {
		t.Fatalf("default = %q", got)
	}
	s.Provider.Model = "claude-pinned"
	if got := defaultModel(s); got != "claude-pinned" {
		t.Fatalf("default = %q", got)
	}
}
