package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMCPModuleBoundaries(t *testing.T) {
	for _, legacy := range []string{"internal/mcpserver", "internal/mcpproto", "internal/mcpconfig"} {
		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Errorf("MCP scope still split at %s: %v", legacy, err)
		}
	}
	for _, root := range []string{"internal/mcp", "internal/memory/manager", "internal/delegation/coordinator", "internal/automation", "internal/controller"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			mcpImports := map[string]bool{}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if root == "internal/mcp" {
					for _, forbidden := range []string{"miya/internal/controller", "miya/internal/ui", "miya/internal/mobile", "miya/internal/conversation", "miya/internal/widgets", "miya/internal/appmodel", "miya/internal/agent/claude/native", "miya/internal/agent/codex/native"} {
						if name == forbidden || strings.HasPrefix(name, forbidden+"/") {
							t.Errorf("%s: MCP depends on implementation %s", path, name)
						}
					}
				} else if root != "internal/controller" && (name == "miya/internal/mcp" || strings.HasPrefix(name, "miya/internal/mcp/")) {
					t.Errorf("%s: domain service depends on MCP adapter %s", path, name)
				}
				if name == "miya/internal/mcp" {
					alias := "mcp"
					if imp.Name != nil {
						alias = imp.Name.Name
					}
					mcpImports[alias] = true
				}
			}
			if root == "internal/controller" {
				ast.Inspect(file, func(node ast.Node) bool {
					if call, ok := node.(*ast.CallExpr); ok {
						if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Register" {
							if pkg, ok := selector.X.(*ast.Ident); ok && mcpImports[pkg.Name] {
								t.Errorf("%s: Controller registers MCP tools", path)
							}
						}
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
