package appmodel

import (
	"miya/locales"
)

// tr translates English interface copy; see locales.T.
func tr(english string) string { return locales.T(english) }

// Tr translates copy used by integration fixtures.
func Tr(english string) string { return tr(english) }
