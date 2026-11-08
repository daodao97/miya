package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/notification"
	"miya/internal/prefs"
	"miya/locales"
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
	for _, path := range []string{"mygo.json", "resources/ios/MiyaFiles.swift"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if containsHan(string(data)) {
			t.Errorf("%s: native product copy must be English", path)
		}
	}
}

func TestLegacyServicePreferenceDoesNotChooseDeviceLanguage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := prefs.SettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"locale":"zh","permissionPolicy":"never"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if prefs.LoadLocale() != "en" || prefs.LoadPermissionPolicy() != "never" {
		t.Fatal("legacy language preference affected English UI or permissions")
	}
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetLocale("zh") // A legacy service or preference cannot change the copy.
	app.Section = "Settings"
	tt.Frame()
	if !tt.HasText("General") || !tt.HasText("Appearance") || !tt.HasText("Interface language") || !tt.HasText("Follow system") {
		t.Fatalf("settings language: %q", tt.Texts())
	}
	userText := "帮我检查中文文件"
	if got := notification.Task(model.Thread{Messages: []model.Message{{Role: "user", Content: userText}}}); got != "Task: "+userText {
		t.Fatalf("user text was changed: %q", got)
	}
}
