package main

import (
	"miya/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

var applyInterfaceLanguage = func(tag string) { mygo.App.SetLocale(tag) }

func configureInterfaceLanguage() {
	if saved := loadSetting("interfaceLanguage"); saved != "" {
		applyInterfaceLanguage(normalizeInterfaceLanguage(saved))
	}
}

func setInterfaceLanguage(tag string) {
	tag = normalizeInterfaceLanguage(tag)
	saved := tag
	if saved == "" {
		saved = "system"
	}
	persistSetting("interfaceLanguage", saved)
	applyInterfaceLanguage(tag)
}

func useInterfaceLanguage(c *ui.Context) {
	locales.SetCurrent(c.Locale())
}

func languageSettings(c *ui.Context) {
	settingRow(c, tr("Interface language"), tr("Changes apply immediately on this device."), func() { languageOptions(c) })
}

func languageOptions(c *ui.Context) {
	ids := []string{"", "zh-Hans", "en"}
	selected := 0
	saved := normalizeInterfaceLanguage(loadSetting("interfaceLanguage"))
	for i, tag := range ids {
		if tag == saved {
			selected = i
		}
	}
	before := selected
	ui.Segmented(c.Key("interface-language"), &selected, tr("Follow system"), i18n.Autonym("zh-Hans"), i18n.Autonym("en")).Label(tr("Interface language"))
	if selected != before {
		setInterfaceLanguage(ids[selected])
	}
}
