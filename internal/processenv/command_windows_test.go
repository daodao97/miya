package processenv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func init() {
	if os.Getenv("MIYA_NODE_SHIM_TEST_HELPER") == "1" {
		json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		os.Exit(0)
	}
}
func TestWindowsNodeShimPreservesPromptArguments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node with spaces & symbols")
	shim := filepath.Join(root, "codex.cmd")
	writeFixture(t, filepath.Join(root, "node_modules/@openai/codex/package.json"), `{"name":"@openai/codex","bin":{"codex":"bin/codex.js"}}`)
	entry := filepath.Join(root, "node_modules/@openai/codex/bin/codex.js")
	writeFixture(t, entry, "// fixture")
	writeFixture(t, shim, `@node "%dp0%\node_modules\@openai\codex\bin\codex.js" %*`)
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "node.exe"), b, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{`prompt with & | %PATH% ! and "quotes"`, "line\nbreak", `C:\project space\`}
	cmd := CommandContext(context.Background(), shim, args...)
	cmd.Env = append(os.Environ(), "MIYA_NODE_SHIM_TEST_HELPER=1")
	Prepare(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	var got []string
	if json.Unmarshal(out, &got) != nil || !reflect.DeepEqual(got, append([]string{entry}, args...)) {
		t.Fatalf("arguments changed: %s", out)
	}
}
func TestWindowsBatchRefusesExpansion(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "custom.cmd")
	writeFixture(t, shim, "@echo test")
	for _, arg := range []string{"%PATH%", "line\nbreak", `a"quote`} {
		cmd := CommandContext(context.Background(), shim, arg)
		Prepare(cmd)
		if cmd.Err == nil {
			t.Fatalf("accepted batch expansion: %q", arg)
		}
	}
}
