// Package extractortest provides fakes for tests of memory extraction.
package extractortest

import (
	"os"
	"path/filepath"
	"testing"
)

// FakeCodex installs a fake Codex CLI as CODEX_BINARY for the test. It
// answers the feature and MCP listings and, for exec, records its arguments,
// directory and input in the returned log file and replies with no
// candidates. MIYA_CODEX_TEST_FAIL makes exec fail as a broken configuration
// does; MIYA_CODEX_TEST_ITEM sets the item type it reports.
func FakeCodex(t testing.TB) (bin, log string) {
	t.Helper()
	root := t.TempDir()
	bin = filepath.Join(root, "codex.py")
	log = filepath.Join(root, "log.json")
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
