package integration

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
)

// All presentation packages consume shared contracts, including phone and shared
// widgets. Inspect Go syntax rather than an incomplete filename/text convention.
func TestViewsDoNotKnowAgents(t *testing.T) {
	for _, dir := range []string{"internal/ui", "internal/mobile", "internal/chat/view", "internal/widgets", "internal/agent"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, native := range []string{"miya/internal/controller", "miya/internal/agent/codex", "miya/internal/agent/claude"} {
					if name == native || strings.HasPrefix(name, native+"/") {
						t.Errorf("%s: imports implementation %s", path, name)
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && (selector.Sel.Name == "AgentCodex" || selector.Sel.Name == "AgentClaude") {
					t.Errorf("%s: selects a concrete agent", fs.Position(node.Pos()))
				}
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					value, _ := strconv.Unquote(literal.Value)
					if value == "codex" || value == "claude" || value == "claude-code" || strings.HasPrefix(value, "item/") || strings.HasPrefix(value, "mcpServer/") {
						t.Errorf("%s: contains native routing value %q", fs.Position(node.Pos()), value)
					}
				}
				return true
			})
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

// The built-in manifest and runtime factory must agree. A new integration
// adds its declaration and implementation, with no edits to either UI.
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

func TestAgentImplementationsDoNotImportControllerOrViews(t *testing.T) {
	for _, dir := range []string{"internal/agent/codex", "internal/agent/claude"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				for _, forbidden := range []string{"miya/internal/controller", "miya/internal/ui", "miya/internal/mobile", "miya/internal/appmodel"} {
					if name == forbidden {
						t.Errorf("%s: imports host implementation %s", path, name)
					}
				}
			}
		}
	}
}
