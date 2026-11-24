package policy

import (
	"encoding/json"
	"reflect"
	"testing"
)

// A Bot's other projects become writable roots of a workspaceWrite turn,
// next to the ones Codex resolved; other sandboxes are left alone.
func TestWithWritableRoots(t *testing.T) {
	resolved := json.RawMessage(`{"type":"workspaceWrite","networkAccess":false,"writableRoots":["/tmp/a"]}`)
	got, ok := WithWritableRoots(resolved, []string{"/work/b", "/tmp/a"}).(map[string]any)
	if !ok || !reflect.DeepEqual(got["writableRoots"], []any{"/tmp/a", "/work/b"}) || got["networkAccess"] != false {
		t.Fatalf("resolved sandbox became %v", got)
	}
	base := TurnSandboxForPolicy("on-request")
	if got := WithWritableRoots(base, []string{"/work/b"}).(map[string]any); !reflect.DeepEqual(got["writableRoots"], []any{"/work/b"}) {
		t.Fatalf("base sandbox became %v", got)
	}
	if _, has := base["writableRoots"]; has {
		t.Fatal("the base sandbox was changed in place")
	}
	for _, policy := range []string{"never", "read"} {
		sb := TurnSandboxForPolicy(policy)
		if got := WithWritableRoots(sb, []string{"/work/b"}).(map[string]any); got["writableRoots"] != nil {
			t.Errorf("%s sandbox got roots: %v", policy, got)
		}
	}
	if got := WithWritableRoots(resolved, nil); string(got.(json.RawMessage)) != string(resolved) {
		t.Fatal("no roots changed the sandbox")
	}
}
