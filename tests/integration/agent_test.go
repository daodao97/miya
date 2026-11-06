package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/codex"
	"miya/internal/controller"
	"miya/internal/model"
)

// The interface is shared by every agent: views reach agents only through
// agentBackend, never by an agent's ID or its adapter's state.
func TestViewsDoNotKnowAgents(t *testing.T) {
	files, _ := filepath.Glob("internal/ui/view_*.go")
	files = append(files, "internal/ui/app.go")
	forbidden := []string{`"claude-code"`, `"codex"`, "agentCodex", "agentClaude", "currentAgent() ==", "currentAgent() !=",
		".claudeError", ".claudeBinary", ".claudeCommand", ".claudeRuns", "sendClaude", "sendCodex"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, s := range forbidden {
				if strings.Contains(line, s) {
					t.Errorf("%s:%d mentions %s: %s", f, i+1, s, strings.TrimSpace(line))
				}
			}
		}
	}
}

func TestAgentRegistry(t *testing.T) {
	c := &controller.Controller{}
	seen := map[string]bool{}
	for _, b := range c.Adapters() {
		if b.ID() == "" || seen[b.ID()] || b.Name() == "" {
			t.Fatalf("bad agent %q %q", b.ID(), b.Name())
		}
		seen[b.ID()] = true
		if spec := b.Icon(); spec.SVG == "" && len(spec.PNG) == 0 {
			t.Errorf("%s has no icon", b.ID())
		}
		for _, s := range b.Settings() {
			if s.ID == "" || s.Title == "" || s.Value == nil || s.Save == nil {
				t.Errorf("%s: incomplete setting %+v", b.ID(), s)
			}
		}
	}
	if model.ThreadAgent(model.Thread{}) != appmodel.AgentCodex || c.BackendFor("unknown").ID() != c.Adapters()[0].ID() {
		t.Fatal("defaults")
	}
	claudeID, _ := json.Marshal("claude:3:r1")
	if c.ApprovalOwner(codex.Approval{ID: claudeID}).ID() != appmodel.AgentClaude || c.ApprovalOwner(codex.Approval{ID: json.RawMessage("7")}).ID() != appmodel.AgentCodex {
		t.Fatal("approval routing")
	}
}

// ThreadRef.Valid checks backends against model.Backends, which cannot see
// agentBackends. Adding an adapter must add its ID there too.
func TestModelBackendsMatchAgentBackends(t *testing.T) {
	c := &controller.Controller{}
	var ids []string
	for _, b := range c.Adapters() {
		ids = append(ids, b.ID())
	}
	if strings.Join(ids, ",") != strings.Join(model.Backends, ",") {
		t.Fatalf("model.Backends = %v, agentBackends = %v", model.Backends, ids)
	}
	if got, want := model.AgentName("unknown"), c.BackendFor("unknown").Name(); got != want {
		t.Fatalf("model.AgentName falls back to %q, backendFor to %q", got, want)
	}
}
