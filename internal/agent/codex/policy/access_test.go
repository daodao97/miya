package policy

import "testing"

// An automation's access levels map to Codex sandboxes that make it ask
// Miya, never to full access.
func TestAccessLevels(t *testing.T) {
	if got := SandboxForPolicy("read"); got != "read-only" {
		t.Fatalf("read sandbox: %s", got)
	}
	if got := TurnSandboxForPolicy("read")["type"]; got != "readOnly" {
		t.Fatalf("read turn sandbox: %v", got)
	}
	if got := SandboxForPolicy("edit"); got != "workspace-write" {
		t.Fatalf("edit sandbox: %s", got)
	}
	for _, p := range []string{"read", "edit"} {
		if ApprovalForPolicy(p) != "on-request" || ReviewerForPolicy(p) != "user" {
			t.Fatalf("%s approvals: %s %s", p, ApprovalForPolicy(p), ReviewerForPolicy(p))
		}
	}
	if Verify("read", raw(`{"type":"workspaceWrite"}`), raw(`"on-request"`)) == nil {
		t.Fatal("a writable sandbox passed for read")
	}
	if err := Verify("read", raw(`{"type":"readOnly"}`), raw(`"on-request"`)); err != nil {
		t.Fatal(err)
	}
	if Verify("edit", raw(`{"type":"workspaceWrite"}`), raw(`"never"`)) == nil {
		t.Fatal("approval never passed for edit")
	}
}
