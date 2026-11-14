package nsui

import (
	"strings"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/prefs"
	"miya/locales"
)

// interfaceLanguage points Miya's catalog at the language the app runs in,
// the saved choice or else the system's, and returns it. tr reads it while
// views build, so a view that calls this rebuilds when the language changes.
func interfaceLanguage(cx *core.Scope) string {
	locale := cx.Env().Locale()
	tag := prefs.NormalizeInterfaceLanguage(locale)
	if tag == "" {
		tag = "en"
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			tag = "zh-Hans"
		}
	}
	locales.SetCurrent(tag)
	return tag
}
