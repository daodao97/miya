package integration

import (
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
		"nsui":       {"controller", "nsmobile"},
		"nsmobile":   {"controller", "nsui"},
		"nschat":     {"controller", "nsui", "nsmobile"},
		"nswidgets":  {"controller", "nsui", "nsmobile", "nschat", "nsrequests"},
		"nsrequests": {"controller", "nsui", "nsmobile", "nschat"},
		"controller": {"nsui", "nsmobile", "nschat", "nswidgets", "nsrequests"},
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

// Retired UI dependencies must stay absent from every integration source,
// including platform-specific files that are not selected on this host.
func TestIntegrationImportsAreIndependentOfMyGo(t *testing.T) {
	files, err := filepath.Glob("tests/integration/*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
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
					t.Errorf("%s imports retired UI %s", path, name)
				}
			}
		}
	}
}
