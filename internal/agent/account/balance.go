package account

import (
	"regexp"
	"strings"
)

var decimal = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// FormatBalance shows a credit balance as Codex reports it, a decimal string
// such as "62500.0000000000", without the trailing zeros of its fraction:
// "62500", "12.5". Anything else is shown as it is.
func FormatBalance(s string) string {
	t := strings.TrimSpace(s)
	if !decimal.MatchString(t) || !strings.Contains(t, ".") {
		return s
	}
	t = strings.TrimRight(strings.TrimRight(t, "0"), ".")
	if t == "-0" {
		t = "0"
	}
	return t
}
