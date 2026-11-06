package text

import (
	"strings"

	"miya/locales"
)

func ClippedTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 45 {
		return string(r[:45]) + "…"
	}
	return s
}

func tr(s string) string { return locales.T(s) }
