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

func TestSharedDomainPackagesDoNotImportAgentImplementations(t *testing.T) {
	for _, root := range []string{"chat/model", "chat/presentation", "chat/text", "chat/subagent", "agent/account", "agent/quota", "agent/catalog", "service/server", "service/client", "service/protocol", "service/process", "service/workspaces"} {
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
