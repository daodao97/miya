package integration

import (
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Keep the surfaces independently replaceable: the executable connects them
// to controllers through Host, and both render through conversation.
func TestUIPackageBoundaries(t *testing.T) {
	for dir, forbidden := range map[string][]string{
		"nsui":       {"controller", "nsmobile", "ui", "mobile", "chat/view", "widgets"},
		"nsmobile":   {"controller", "nsui", "ui", "mobile", "chat/view", "widgets"},
		"nschat":     {"controller", "nsui", "nsmobile", "ui", "mobile", "chat/view", "widgets"},
		"nswidgets":  {"controller", "nsui", "nsmobile", "nschat", "nsrequests", "ui", "mobile", "chat/view", "widgets"},
		"nsrequests": {"controller", "nsui", "nsmobile", "nschat", "ui", "mobile", "chat/view", "widgets"},
		"controller": {"ui", "mobile", "chat/view", "widgets", "nsui", "nsmobile", "nschat", "nswidgets", "nsrequests"},
	} {
		files, err := filepath.Glob(filepath.Join("internal", dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("missing package %s: %v", dir, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, blocked := range forbidden {
					if path == "miya/internal/"+blocked || strings.HasPrefix(path, "miya/internal/"+blocked+"/") {
						t.Errorf("%s imports %s across the Host boundary", file, path)
					}
				}
			}
		}
	}
}

// The historical suite remains opt-in. Deleting its packages must not silently
// reintroduce a framework dependency through an otherwise unrelated test helper.
func TestDefaultIntegrationImportsAreIndependentOfMyGo(t *testing.T) {
	for _, tags := range [][]string{nil, {"nsgo"}} {
		ctx := build.Default
		ctx.BuildTags = tags
		files, err := filepath.Glob("tests/integration/*_test.go")
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			selected, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				t.Fatal(err)
			}
			if !selected {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, blocked := range []string{"github.com/egoist/mygo", "miya/internal/ui", "miya/internal/mobile", "miya/internal/widgets", "miya/internal/chat/view"} {
					if name == blocked || strings.HasPrefix(name, blocked+"/") {
						t.Errorf("tags %v: %s imports legacy UI %s", tags, path, name)
					}
				}
			}
		}
	}
}
