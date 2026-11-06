package widgets

import (
	appmodel "miya/internal/appmodel"
	locales "miya/locales"

	mygo "github.com/egoist/mygo"
	i18n "github.com/egoist/mygo/i18n"
	ui "github.com/egoist/mygo/ui"
)

var ApplyInterfaceLanguage = func(tag string) { mygo.App.SetLocale(tag) }

func ConfigureInterfaceLanguage() {
	if saved := appmodel.LoadSetting("interfaceLanguage"); saved != "" {
		ApplyInterfaceLanguage(appmodel.NormalizeInterfaceLanguage(saved))
	}
}

func SetInterfaceLanguage(tag string) {
	tag = appmodel.NormalizeInterfaceLanguage(tag)
	saved := tag
	if saved == "" {
		saved = "system"
	}
	appmodel.PersistSetting("interfaceLanguage", saved)
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
	saved := appmodel.NormalizeInterfaceLanguage(appmodel.LoadSetting("interfaceLanguage"))
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
