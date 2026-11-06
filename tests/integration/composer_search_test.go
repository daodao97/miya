package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/chat/composer"
	projectfiles "miya/internal/project/files"
)

func TestComposerCompletionPreservesPrompt(t *testing.T) {
	before, after := "请检查  的错误", "请检查 @main 的错误"
	token := composer.ComposerTokenAt(after, composer.ComposerEditEnd(before, after))
	if token.Kind != '@' || token.Query != "main" {
		t.Fatalf("wrong token: %+v", token)
	}
	if got := composer.InsertFileReference(after, token, "main.go"); got != "请检查 @main.go  的错误" {
		t.Fatal(got)
	}
	text := "比较 @main.go 和 @\"design no"
	token = composer.ComposerTokenAt(text, len(text))
	if token.Query != "design no" {
		t.Fatalf("quoted search: %+v", token)
	}
	if got := composer.InsertFileReference(text, token, "docs/design notes.md"); got != "比较 @main.go 和 @\"docs/design notes.md\" " {
		t.Fatal(got)
	}
}

func TestComposerDoesNotReopenCompletedReferences(t *testing.T) {
	for _, text := range []string{"hello@example.com", "please see @main.go next", "请检查 @\"design notes.md\" 好吗", "hello /model", "@main\nnext"} {
		if tok := composer.ComposerTokenAt(text, len(text)); tok.Kind != 0 {
			t.Errorf("unexpected completion for %q: %+v", text, tok)
		}
	}
	if choices := composer.CommandChoices("mod"); len(choices) != 1 || choices[0].Name != "model" {
		t.Fatal(choices)
	}
	for _, text := range []string{"/tmp/main.go", "/model\nexplain this", "review /model"} {
		if _, ok := composer.SlashCommand(text); ok {
			t.Errorf("intercepted normal prompt %q", text)
		}
	}
}

func TestFileChoicesRankAndBound(t *testing.T) {
	files := []string{"other/main.go", "main.go", "src/main_test.go", "docs/migration.md", "cmd/map.go"}
	got := composer.FileChoices(files, "main")
	if len(got) < 3 || got[0].Name != "main.go" || got[1].Name != "other/main.go" || got[2].Name != "src/main_test.go" {
		t.Fatal(got)
	}
	got = composer.FileChoices(files, "smtg")
	if len(got) != 1 || got[0].Name != "src/main_test.go" {
		t.Fatal(got)
	}
	for i := 0; i < 100; i++ {
		files = append(files, strings.Repeat("x", i)+".go")
	}
	if len(composer.FileChoices(files, "")) != 30 {
		t.Fatal("unbounded search results")
	}
}

func TestProjectIndex(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"main.go", "docs/设计 笔记.md", ".git/secret", "node_modules/huge.js", ".quickgui/generated.go", "dist/app"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "rg", true: "without-rg"}[fallback], func(t *testing.T) {
			if fallback {
				t.Setenv("PATH", t.TempDir())
			}
			files, err := projectfiles.IndexProjectFiles(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 2 {
				t.Fatalf("index includes generated/private paths: %v", files)
			}
			if len(composer.FileChoices(files, "设计")) != 1 {
				t.Fatalf("unicode filename missing: %v", files)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := projectfiles.IndexProjectFiles(ctx, root); err == nil {
				t.Fatal("cancelled search succeeded")
			}
		})
	}
}

func TestFileMentionInputs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	got := composer.FileMentionInputs("请检查 @main.go、@missing 和 test@example.com", root)
	if len(got) != 1 || got[0]["type"] != "mention" || got[0]["name"] != "main.go" {
		t.Fatalf("unexpected mentions: %#v", got)
	}
	got = composer.FileMentionInputs(`查看 @"docs"`, root)
	if len(got) != 1 || got[0]["name"] != "docs" {
		t.Fatalf("quoted directory: %#v", got)
	}
}
