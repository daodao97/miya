package runtime

import (
	"regexp"
	"strings"
)

// Store a bounded, redacted description; raw command/output remains in the
// agent's existing transcript. Common assignments and credential flags vanish.
var secretValue = regexp.MustCompile(`(?i)((?:[A-Z0-9_]*(?:TOKEN|PASSWORD|SECRET|API_KEY)[A-Z0-9_]*|--(?:token|password|secret|api-key))\s*(?:=|\s)\s*)(?:"[^"]*"|'[^']*'|[^\s;]+)`)
var bearerValue = regexp.MustCompile(`(?i)(bearer\s+)[^\s"']+`)

func Summary(value string) string {
	value = secretValue.ReplaceAllString(value, "${1}[redacted]")
	value = bearerValue.ReplaceAllString(value, "${1}[redacted]")
	value = strings.TrimSpace(strings.SplitN(value, "\n", 2)[0])
	r := []rune(value)
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return value
}
