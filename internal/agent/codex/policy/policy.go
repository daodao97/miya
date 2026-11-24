package policy

import (
	"encoding/json"
	"errors"
	"fmt"

	agentpolicy "miya/internal/agent/policy"
)

// "on-failure" is retained as the stored ID for the existing Help me approve
// preference; it must not be sent as the protocol's deprecated failure policy.
func ApprovalForPolicy(policy string) string {
	if agentpolicy.Normalize(policy) == "never" {
		return "never"
	}
	return "on-request"
}

func SandboxForPolicy(policy string) string {
	switch policy {
	case "never":
		return "danger-full-access"
	case agentpolicy.Read:
		return "read-only"
	}
	return "workspace-write"
}

func ReviewerForPolicy(policy string) string {
	if policy == "on-failure" {
		return "auto_review"
	}
	return "user"
}

func TurnSandboxForPolicy(policy string) map[string]any {
	switch policy {
	case "never":
		return map[string]any{"type": "dangerFullAccess"}
	case agentpolicy.Read:
		return map[string]any{"type": "readOnly"}
	}
	return map[string]any{"type": "workspaceWrite", "networkAccess": false}
}

// TurnSandbox is the sandboxPolicy for turn/start. Codex keeps a turn's
// override for every later turn, so while the mode is unchanged the policy
// Codex resolved at thread start or resume is sent back as is: it carries the
// person's writable roots and network setting from config.toml.
func TurnSandbox(policy string, resolved json.RawMessage) any {
	base := TurnSandboxForPolicy(policy)
	if sandboxType(resolved) == base["type"] {
		return resolved
	}
	return base
}

// Ranked from most to least restrictive.
var sandboxRank = map[string]int{"readOnly": 0, "workspaceWrite": 1, "externalSandbox": 2, "dangerFullAccess": 3}
var approvalRank = map[string]int{"untrusted": 0, "on-request": 1, "never": 2}

func sandboxType(raw json.RawMessage) string {
	var s struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s.Type
}

// Verify fails closed when the app-server reports no sandbox, or a sandbox or
// approval policy more permissive than policy asked for. A stricter one, as
// managed requirements may impose, is accepted.
func Verify(policy string, sandbox, approval json.RawMessage) error {
	want := TurnSandboxForPolicy(policy)["type"].(string)
	got := sandboxType(sandbox)
	rank, known := sandboxRank[got]
	if !known {
		return errors.New("Codex did not report the sandbox it applied, so its permissions cannot be verified; update Codex and retry")
	}
	if rank > sandboxRank[want] {
		return fmt.Errorf("Codex applied a %s sandbox where %s was requested; nothing was sent", got, want)
	}
	var name string
	// A granular policy can turn sandbox prompts off, so it ranks as "never".
	gotApproval := approvalRank["never"]
	if json.Unmarshal(approval, &name) == nil {
		r, ok := approvalRank[name]
		if !ok {
			return fmt.Errorf("Codex reported an unknown approval policy %q; nothing was sent", name)
		}
		gotApproval = r
	} else if len(approval) == 0 {
		return errors.New("Codex did not report the approval policy it applied, so its permissions cannot be verified; update Codex and retry")
	}
	if wantApproval := ApprovalForPolicy(policy); gotApproval > approvalRank[wantApproval] {
		return fmt.Errorf("Codex applied approval policy %s where %s was requested; nothing was sent", string(approval), wantApproval)
	}
	return nil
}

// WithWritableRoots adds roots to a workspaceWrite turn sandbox, for a Bot
// that also works in other projects. Other sandboxes already read them, or
// reach everything.
func WithWritableRoots(sandbox any, roots []string) any {
	if len(roots) == 0 {
		return sandbox
	}
	var m map[string]any
	switch v := sandbox.(type) {
	case map[string]any:
		m = make(map[string]any, len(v)+1)
		for k, x := range v {
			m[k] = x
		}
	case json.RawMessage:
		if json.Unmarshal(v, &m) != nil {
			return sandbox
		}
	default:
		return sandbox
	}
	if m["type"] != "workspaceWrite" {
		return sandbox
	}
	var all []any
	seen := map[string]bool{}
	if existing, ok := m["writableRoots"].([]any); ok {
		for _, r := range existing {
			if s, ok := r.(string); ok && !seen[s] {
				seen[s] = true
				all = append(all, s)
			}
		}
	}
	for _, r := range roots {
		if !seen[r] {
			seen[r] = true
			all = append(all, r)
		}
	}
	m["writableRoots"] = all
	return m
}
