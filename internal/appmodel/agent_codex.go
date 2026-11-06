package appmodel

import (
	_ "embed"
)

//go:embed assets/agents/codex.png
var CodexAgentPNG []byte

// codexAccountLimitID names the account-wide rate limit bucket; other
// buckets are a model's own.
const CodexAccountLimitID = "codex"
