package widgets

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"miya/internal/prefs"
	"miya/locales"
)

var ApplyInterfaceLanguage = func(tag string) { mygo.App.SetLocale(tag) }

func ConfigureInterfaceLanguage() {
	if saved := prefs.Load("interfaceLanguage"); saved != "" {
		ApplyInterfaceLanguage(prefs.NormalizeInterfaceLanguage(saved))
	}
}

func SetInterfaceLanguage(tag string) {
	tag = prefs.NormalizeInterfaceLanguage(tag)
	saved := tag
	if saved == "" {
		saved = "system"
	}
	prefs.Save("interfaceLanguage", saved)
	ApplyInterfaceLanguage(tag)
}

func UseInterfaceLanguage(c *ui.Context) {
	locales.SetCurrent(c.Locale())
}

func LanguageSettings(c *ui.Context) {
	SettingRow(c, tr("Interface language"), tr("Changes apply immediately on this device."), func() { languageOptions(c) })
}

func languageOptions(c *ui.Context) {
	ids := []string{"", "zh-Hans", "en"}
	selected := 0
	saved := prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage"))
	for i, tag := range ids {
		if tag == saved {
			selected = i
		}
	}
	before := selected
	ui.Segmented(c.Key("interface-language"), &selected, tr("Follow system"), i18n.Autonym("zh-Hans"), i18n.Autonym("en")).Label(tr("Interface language"))
	if selected != before {
		SetInterfaceLanguage(ids[selected])
	}
}
