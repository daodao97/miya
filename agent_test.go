package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/codex"
	"miya/internal/model"
)

// The interface is shared by every agent: views reach agents only through
// agentBackend, never by an agent's ID or its adapter's state.
func TestViewsDoNotKnowAgents(t *testing.T) {
	files, _ := filepath.Glob("view_*.go")
	files = append(files, "app.go")
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
	seen := map[string]bool{}
	for _, b := range agentBackends {
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
	if model.ThreadAgent(Thread{}) != agentCodex || backendFor("unknown").ID() != agentBackends[0].ID() {
		t.Fatal("defaults")
	}
	claudeID, _ := json.Marshal("claude:3:r1")
	if approvalOwner(codex.Approval{ID: claudeID}).ID() != agentClaude || approvalOwner(codex.Approval{ID: json.RawMessage("7")}).ID() != agentCodex {
		t.Fatal("approval routing")
	}
}

// ThreadRef.Valid checks backends against model.Backends, which cannot see
// agentBackends. Adding an adapter must add its ID there too.
func TestModelBackendsMatchAgentBackends(t *testing.T) {
	var ids []string
	for _, b := range agentBackends {
		ids = append(ids, b.ID())
	}
	if strings.Join(ids, ",") != strings.Join(model.Backends, ",") {
		t.Fatalf("model.Backends = %v, agentBackends = %v", model.Backends, ids)
	}
}
