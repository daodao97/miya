package integration

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Scan source imports across all platform files so a helper domain cannot
// silently link a native agent back into either surface.
func TestUITransitiveDependenciesStayProviderNeutral(t *testing.T) {
	visited := map[string]bool{}
	var visit func(string, []string)
	visit = func(pkg string, chain []string) {
		if visited[pkg] {
			return
		}
		visited[pkg] = true
		chain = append(append([]string(nil), chain...), pkg)
		for _, blocked := range []string{"miya/internal/agent/codex", "miya/internal/agent/claude", "miya/internal/agent/integrations", "miya/internal/controller"} {
			if pkg == blocked || strings.HasPrefix(pkg, blocked+"/") {
				t.Errorf("UI depends on implementation: %s", strings.Join(chain, " -> "))
				return
			}
		}
		if !strings.HasPrefix(pkg, "miya/internal/") {
			return
		}
		dir := strings.TrimPrefix(pkg, "miya/")
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				visit(name, chain)
			}
		}
	}
	for _, root := range []string{"miya/internal/ui", "miya/internal/mobile", "miya/internal/chat/view"} {
		visit(root, nil)
	}
}

func TestSharedDomainPackagesDoNotImportAgentImplementations(t *testing.T) {
	for _, root := range []string{"chat/model", "chat/presentation", "chat/text", "chat/subagent", "agent/account", "agent/quota", "agent/catalog", "service/server", "service/client", "service/protocol", "service/process", "service/workspaces", "project/model", "project/files", "project/git", "project/registry", "project/access", "agent/policy", "runtime"} {
		err := filepath.WalkDir(filepath.Join("internal", root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				for _, blocked := range []string{"miya/internal/agent/codex", "miya/internal/agent/claude", "miya/internal/agent/integrations", "miya/internal/controller", "miya/internal/ui", "miya/internal/mobile"} {
					if name == blocked || strings.HasPrefix(name, blocked+"/") {
						t.Errorf("%s imports implementation %s", path, name)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
