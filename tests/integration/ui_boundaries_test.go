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
		"ui":           {"controller", "mobile"},
		"mobile":       {"controller", "ui"},
		"conversation": {"controller", "ui", "mobile"},
		"controller":   {"ui", "mobile", "conversation", "widgets"},
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
					if path == "miya/internal/"+blocked {
						t.Errorf("%s imports %s across the Host boundary", file, path)
					}
				}
			}
		}
	}
}
