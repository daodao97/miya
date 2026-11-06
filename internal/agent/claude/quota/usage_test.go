package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"miya/internal/agent/quota/testcli"
)

func TestMain(m *testing.M) { testcli.Main(m) }
func TestClaudeUsageIsolationAndWindows(t *testing.T) {
	windows, err := readClaude(context.Background(), testcli.Helper(t, "claude"), t.TempDir())
	if err != nil || len(windows) != 2 || windows[0].Used != 13 || windows[1].Used != 42 || !strings.Contains(windows[0].Reset, "Asia/Shanghai") {
		t.Fatalf("%+v %v", windows, err)
	}
}

func TestClaudeUnavailableNeverBecomesZero(t *testing.T) {
	for _, text := range []string{"", "You are currently using your subscription to power your Claude Code usage", "Current session: 1% used\nError 401 unauthorized", "Current week (Sonnet): 5% used"} {
		data, _ := json.Marshal(map[string]string{"type": "result", "result": text})
		if windows, err := parseClaude(data); err == nil || len(windows) != 0 {
			t.Fatalf("accepted unavailable %q: %+v %v", text, windows, err)
		}
	}
	data := []byte(`[{"type":"system"},{"type":"result","result":"\u001b[32mCurrent session: 0% used\u001b[0m"}]`)
	if windows, err := parseClaude(data); err != nil || len(windows) != 1 || windows[0].Used != 0 {
		t.Fatalf("real zero: %+v %v", windows, err)
	}
}

func TestOutputBound(t *testing.T) {
	var b limitedBuffer
	if _, err := fmt.Fprint(&b, strings.Repeat("a", 1<<20+1)); err == nil || b.Len() > 1<<20 {
		t.Fatal("output not bounded")
	}
}
