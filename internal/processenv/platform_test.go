package processenv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestWSLTargetRoundTrip(t *testing.T) {
	original := WSLTarget{Distribution: `Ubuntu space & value`, Binary: `/home/user name/bin/codex`}
	encoded := WSLBinary(original.Distribution, original.Binary)
	got, ok := WSL(encoded)
	if !ok || got != original {
		t.Fatalf("lost target: %+v %v", got, ok)
	}
	if _, ok := WSL("wsl:invalid"); ok {
		t.Fatal("accepted invalid target")
	}
}
func TestProtocolPathsPreserveContentAndNumbers(t *testing.T) {
	bin := WSLBinary("Ubuntu", "codex")
	source := []byte(`{"id":9007199254740993,"cwd":"C:\\project space","input":[{"path":"C:\\photos\\a.png","text":"keep C:\\photos\\a.png and /home/me/file"}],"writableRoots":["\\\\wsl.localhost\\Ubuntu\\home\\me"],"url":"https://example.test/path"}`)
	mapped := ProtocolPaths(source, bin, true)
	if !strings.Contains(string(mapped), `"cwd":"/mnt/c/project space"`) || !strings.Contains(string(mapped), `"path":"/mnt/c/photos/a.png"`) || !strings.Contains(string(mapped), `9007199254740993`) || !strings.Contains(string(mapped), `"writableRoots":["/home/me"]`) {
		t.Fatal(string(mapped))
	}
	var value map[string]json.RawMessage
	json.Unmarshal(mapped, &value)
	var items []map[string]string
	json.Unmarshal(value["input"], &items)
	if items[0]["text"] != `keep C:\photos\a.png and /home/me/file` {
		t.Fatal("rewrote user content")
	}
	incoming := ProtocolPaths([]byte(`{"cwd":"/home/me/project","path":"/mnt/d/project/a.txt","text":"/home/me"}`), bin, false)
	if !strings.Contains(string(incoming), `\\\\wsl.localhost\\Ubuntu\\home\\me\\project`) || !strings.Contains(string(incoming), `D:\\project\\a.txt`) || !strings.Contains(string(incoming), `"text":"/home/me"`) {
		t.Fatal(string(incoming))
	}
	if got := ProtocolPaths(source, "native", true); string(got) != string(source) {
		t.Fatal("native protocol changed")
	}
}
func writeFixture(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0700); err != nil {
		t.Fatal(err)
	}
}
func TestNodeShimRequiresManifestAndMatchingEntry(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "codex.cmd")
	writeFixture(t, filepath.Join(root, "node_modules/@openai/codex/package.json"), `{"name":"@openai/codex","bin":{"codex":"bin/codex.js"}}`)
	target := filepath.Join(root, "node_modules/@openai/codex/bin/codex.js")
	writeFixture(t, target, "// fixture")
	writeFixture(t, shim, `@node "%dp0%\node_modules\@openai\codex\bin\codex.js" %*`)
	if got := NodeShim(shim); got != target {
		t.Fatalf("shim not resolved: %q", got)
	}
	writeFixture(t, shim, `@echo unrelated wrapper`)
	if NodeShim(shim) != "" {
		t.Fatal("claimed unrelated wrapper")
	}
	writeFixture(t, shim, `@node "%dp0%\node_modules\@openai\codex\bin\codex.js" %*`)
	writeFixture(t, filepath.Join(root, "node_modules/@openai/codex/package.json"), `{"name":"different","bin":{"codex":"bin/codex.js"}}`)
	if NodeShim(shim) != "" {
		t.Fatal("claimed unrelated manifest")
	}
}
func TestWSLBootstrapPreservesArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash fixture")
	}
	args := []string{"space and & pipe |", `%PATH% $HOME $(echo unsafe)`, "line\nbreak", `a"quote`, ""}
	command := append([]string{"-lc", wslBootstrap, "miya", "env", "/usr/bin/printf", "%s\n"}, args...)
	out, err := exec.Command("/bin/bash", command...).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != strings.Join(args, "\n")+"\n" {
		t.Fatalf("arguments changed: %q", out)
	}
}
func TestWSLDoesNotReceiveWindowsPath(t *testing.T) {
	env := []string{`PATH=C:\nodejs`, `HOME=C:\Users\me`, `ANTHROPIC_API_KEY=portable`}
	if got := WithPath(env, WSLBinary("Ubuntu", "claude")); !reflect.DeepEqual(got, env) {
		t.Fatal(got)
	}
	if _, err := PinWSL(context.Background(), "native"); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolPathsUseConfiguredMountRoot(t *testing.T) {
	target := WSLTarget{Distribution: "Ubuntu", Binary: "codex", MountRoot: "/windows"}
	b, _ := json.Marshal(target)
	binary := "wsl:" + base64.RawURLEncoding.EncodeToString(b)
	out := ProtocolPaths([]byte(`{"cwd":"C:\\project"}`), binary, true)
	if string(out) != `{"cwd":"/windows/c/project"}` {
		t.Fatal(string(out))
	}
	back := ProtocolPaths(out, binary, false)
	if string(back) != `{"cwd":"C:\\project"}` {
		t.Fatal(string(back))
	}
}
