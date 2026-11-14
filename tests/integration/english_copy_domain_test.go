//go:build !mygo_ui

package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"miya/locales"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// Chinese input and old conversation titles still need to be recognized.
// These are input patterns, never interface copy.
func TestProductSourceCopyUsesEnglishCatalog(t *testing.T) {
	inputPatterns := map[string][]string{
		"internal/notification/text.go": {"继续", "继续吧", "实现吧", "干吧", "好的", "好", "可以", "嗯", "claude code 对话", "新对话"},
		"internal/memory/jobs.go":       {"忘记", "请忘记", "帮我忘记", "不要再记", "删除这条记忆"},
	}
	containsHan := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
	}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "build" || d.Name() == "testdata" || d.Name() == "scripts") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && len(call.Args) == 1 {
				if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "tr" {
					if source, ok := call.Args[0].(*ast.BasicLit); ok && source.Kind == token.STRING {
						value, _ := strconv.Unquote(source.Value)
						if !locales.HasMessage(value) {
							t.Errorf("%s: missing catalog message: %q", fs.Position(source.Pos()), value)
						}
					}
				}
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, _ := strconv.Unquote(lit.Value)
			if !containsHan(value) {
				return true
			}
			for _, pattern := range inputPatterns[filepath.ToSlash(path)] {
				if value == pattern {
					return true
				}
			}
			t.Errorf("%s: product copy must be English: %q", fs.Position(lit.Pos()), value)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"nsgo.yaml", "resources/ios/MiyaFiles.swift"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if containsHan(string(data)) {
			t.Errorf("%s: native product copy must be English", path)
		}
	}
}
