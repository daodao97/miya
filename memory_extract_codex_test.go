//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/claude"
	"miya/internal/memory"
)

func fakeMemoryCodex(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "codex.py")
	log := filepath.Join(root, "log.json")
	script := `#!/usr/bin/env python3
import json,os,sys
a=sys.argv[1:]
if a[:2]==['features','list']:
 print('shell_tool stable true\nhooks stable true\nmulti_agent stable true\napps stable true')
elif a[:2]==['mcp','list']:
 print(json.dumps([{'name':'configured-server'},{'name':'local.tool','transport':{'type':'stdio'}},{'name':'remote','transport':{'type':'streamable_http'}}]))
elif a[0]=='exec':
 if os.environ.get('MIYA_CODEX_TEST_FAIL'):
  sys.stderr.write('Error: failed to load bootstrap configuration\n\nCaused by:\n    url is not supported for stdio\n    in mcp_servers.remote\n')
  sys.exit(1)
 data=json.loads(sys.stdin.read())
 with open(os.environ['MIYA_CODEX_TEST_LOG'],'w') as f:json.dump({'args':a,'cwd':os.getcwd(),'input':data},f)
 result=a[a.index('--output-last-message')+1]
 with open(result,'w') as f:json.dump({'candidates':[]},f)
 print(json.dumps({'type':'item.completed','item':{'type':os.environ.get('MIYA_CODEX_TEST_ITEM','agent_message'),'text':'{"candidates":[]}'}}))
 print(json.dumps({'type':'turn.completed'}))
else:sys.exit(1)
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BINARY", bin)
	t.Setenv("MIYA_CODEX_TEST_LOG", log)
	return bin, log
}

func TestMemoryCodexSelectionUsesIsolatedConfiguredCLI(t *testing.T) {
	_, log := fakeMemoryCodex(t)
	job := memory.ExtractionJob{ExtractorAgent: agentCodex, Session: "source-session", TurnID: "turn", Evidence: []memory.Evidence{{ID: "user", Kind: "user_statement", Text: "一次性调整，无需记住"}}}
	got, err := extractMemory(context.Background(), claude.CLI{Binary: "missing-claude"}, job)
	if err != nil || len(got) != 0 {
		t.Fatalf("Codex dispatch: %+v %v", got, err)
	}
	b, _ := os.ReadFile(log)
	var call struct {
		Args  []string
		Cwd   string
		Input memory.ExtractionJob
	}
	if err = json.Unmarshal(b, &call); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(call.Args, "|")
	for _, required := range []string{"--ephemeral", "--ignore-rules", "--sandbox|read-only", "features.shell_tool=false", "features.hooks=false", "features.multi_agent=false", `mcp_servers={"configured-server"={enabled=false,command="miya-disabled-extractor-mcp"},"local.tool"={enabled=false,command="miya-disabled-extractor-mcp"},"remote"={enabled=false,url="http://127.0.0.1:9/miya-disabled-extractor-mcp"}}`, "tools.view_image=false", "web_search=\"disabled\""} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing boundary %s: %v", required, call.Args)
		}
	}
	if call.Input.Evidence[0].Text != job.Evidence[0].Text || strings.Contains(joined, "--resume") || strings.Contains(joined, "source-session") {
		t.Fatal("source conversation was resumed or input changed")
	}
	if _, err = os.Stat(call.Cwd); !os.IsNotExist(err) {
		t.Fatal("temporary extractor workspace retained")
	}
}

func TestMemoryCodexRejectsUnexpectedToolEvents(t *testing.T) {
	fakeMemoryCodex(t)
	t.Setenv("MIYA_CODEX_TEST_ITEM", "command_execution")
	if _, err := extractMemory(context.Background(), claude.CLI{}, memory.ExtractionJob{ExtractorAgent: agentCodex}); err == nil {
		t.Fatal("Codex operational tool result accepted")
	}
	if _, err := extractMemory(context.Background(), claude.CLI{}, memory.ExtractionJob{ExtractorAgent: "unknown-agent"}); err == nil {
		t.Fatal("unknown agent silently fell back")
	}
}

func TestMemoryCodexStartupFailureKeepsCLIDiagnosis(t *testing.T) {
	fakeMemoryCodex(t)
	t.Setenv("MIYA_CODEX_TEST_FAIL", "1")
	job := memory.ExtractionJob{ExtractorAgent: agentCodex, Session: "source-session", TurnID: "turn", Evidence: []memory.Evidence{{ID: "user", Kind: "user_statement", Text: "secret evidence text"}}}
	_, err := extractMemoryWithCodex(context.Background(), job)
	if err == nil {
		t.Fatal("failed CLI reported success")
	}
	msg := err.Error()
	if !strings.Contains(msg, "url is not supported for stdio in mcp_servers.remote") || strings.Contains(msg, "Caused by:") || strings.Contains(msg, "\n") {
		t.Fatalf("diagnosis not kept as one bounded line: %q", msg)
	}
	if strings.Contains(msg, "secret evidence text") {
		t.Fatalf("extraction input leaked into the error: %q", msg)
	}
}

func TestMemoryCodexStderrSummaryIsBounded(t *testing.T) {
	summary := codexStderrSummary([]byte(strings.Repeat("错误 ", 400)))
	if n := len([]rune(summary)); n > 301 {
		t.Fatalf("summary has %d runes", n)
	}
}

func TestMemoryCodexOfflineEnvReplacesProxySettings(t *testing.T) {
	env := codexOfflineEnv([]string{"PATH=/bin", "https_proxy=http://corp:8080", "NO_PROXY=127.0.0.1,relay.example", "HOME=/h"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, want := range []string{"\nPATH=/bin\n", "\nHOME=/h\n", "\nHTTPS_PROXY=http://127.0.0.1:9\n", "\nhttps_proxy=http://127.0.0.1:9\n", "\nALL_PROXY=http://127.0.0.1:9\n"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, env)
		}
	}
	if strings.Contains(joined, "corp:8080") || strings.Contains(joined, "NO_PROXY") {
		t.Fatalf("inherited proxy settings kept: %v", env)
	}
}
