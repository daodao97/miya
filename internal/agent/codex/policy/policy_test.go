package policy

import (
	"encoding/json"
	"testing"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestVerifyFailsClosed(t *testing.T) {
	cases := []struct {
		name, policy, sandbox, approval string
		ok                              bool
	}{
		{"exact workspace", "on-request", `{"type":"workspaceWrite","networkAccess":true}`, `"on-request"`, true},
		{"exact full access", "never", `{"type":"dangerFullAccess"}`, `"never"`, true},
		{"stricter sandbox", "on-request", `{"type":"readOnly"}`, `"on-request"`, true},
		{"stricter approval", "never", `{"type":"dangerFullAccess"}`, `"untrusted"`, true},
		{"wider sandbox", "on-request", `{"type":"dangerFullAccess"}`, `"on-request"`, false},
		{"external sandbox", "on-request", `{"type":"externalSandbox"}`, `"on-request"`, false},
		{"wider approval", "on-request", `{"type":"workspaceWrite"}`, `"never"`, false},
		{"granular approval", "on-failure", `{"type":"workspaceWrite"}`, `{"granular":{"mcp_elicitations":true,"rules":true,"sandbox_approval":false}}`, false},
		{"granular under full access", "never", `{"type":"dangerFullAccess"}`, `{"granular":{"mcp_elicitations":true,"rules":true,"sandbox_approval":false}}`, true},
		{"no sandbox", "on-request", ``, `"on-request"`, false},
		{"no approval", "on-request", `{"type":"workspaceWrite"}`, ``, false},
		{"unknown approval", "on-request", `{"type":"workspaceWrite"}`, `"sometimes"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Verify(c.policy, raw(c.sandbox), raw(c.approval))
			if (err == nil) != c.ok {
				t.Fatalf("Verify = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestTurnSandboxKeepsResolvedSettings(t *testing.T) {
	resolved := raw(`{"type":"workspaceWrite","networkAccess":true,"writableRoots":["/extra"]}`)
	if got, ok := TurnSandbox("on-request", resolved).(json.RawMessage); !ok || string(got) != string(resolved) {
		t.Fatalf("unchanged mode dropped the resolved sandbox: %v", TurnSandbox("on-request", resolved))
	}
	changed, ok := TurnSandbox("never", resolved).(map[string]any)
	if !ok || changed["type"] != "dangerFullAccess" {
		t.Fatalf("changed mode kept the old sandbox: %v", changed)
	}
	if base, ok := TurnSandbox("on-request", nil).(map[string]any); !ok || base["type"] != "workspaceWrite" {
		t.Fatalf("missing resolution: %v", base)
	}
}
