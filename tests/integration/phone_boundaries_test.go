package integration

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPhonePackageBoundaries(t *testing.T) {
	for root, forbidden := range map[string][]string{
		"phone":          {"controller", "ui", "mobile", "appmodel", "conversation", "widgets"},
		"phone/protocol": {"phone/server", "phone/cache", "phone/proxy", "phone/transfer"},
		"phone/transfer": {"phone/server", "phone/cache", "phone/proxy", "phone/protocol"},
		"phone/server":   {"phone/cache", "phone/proxy"},
		"phone/cache":    {"phone/server", "phone/proxy"},
		"phone/proxy":    {"phone/server", "phone/cache"},
		"mobile":         {"phone/server"},
		"model":          {"phone"},
		"appmodel":       {"phone"},
	} {
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
				for _, blocked := range forbidden {
					prefix := "miya/internal/" + blocked
					if name == prefix || strings.HasPrefix(name, prefix+"/") {
						t.Errorf("%s imports %s across the phone boundary", path, name)
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
