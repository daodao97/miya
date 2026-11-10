package widgets

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// Settings follow macOS System Settings: a titled, rounded group of rows
// split by hairlines inset from the leading edge, an optional footnote under
// the group, regular-weight row titles and secondary text below them.
const (
	settingsRadius = 10
	settingsInset  = 14
)

// SettingsGroup is a titled group of settings rows with an optional note
// under it. Each row gets the group's padding.
func SettingsGroup(c *ui.Context, title, footnote string, rows ...func()) {
	settingsGroup(c, title, footnote, nil, true, rows)
}

// SettingsGroupActions is a SettingsGroup with actions beside its title.
func SettingsGroupActions(c *ui.Context, title, footnote string, actions func(), rows ...func()) {
	settingsGroup(c, title, footnote, actions, true, rows)
}

// SettingsList is a group whose rows draw their own padding, for rows that
// are buttons filling the row, such as a list of choices.
func SettingsList(c *ui.Context, title, footnote string, rows ...func()) {
	settingsGroup(c, title, footnote, nil, false, rows)
}

func settingsGroup(c *ui.Context, title, footnote string, actions func(), padded bool, rows []func()) {
	ui.Column(c.Key("group:" + title)).FillWidth().MinWidth(0).Gap(8).Children(func() {
		if title != "" || actions != nil {
			ui.Row(c).FillWidth().MinWidth(0).MinHeight(22).AlignItems(ui.Center).Gap(8).PaddingX(2).Children(func() {
				ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(Ink).Grow(1).MinWidth(0)
				if actions != nil {
					ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(actions)
				}
			})
		}
		ui.Column(c).FillWidth().MinWidth(0).Background(Panel).Border(1, Line).Radius(settingsRadius).Clip().Children(func() {
			for i, row := range rows {
				if i > 0 {
					SettingsSeparator(c.Key(fmt.Sprint("sep", i)))
				}
				box := ui.Column(c.Key(fmt.Sprint("row", i))).FillWidth().MinWidth(0)
				if padded {
					box.MinHeight(44).Justify(ui.Center).Padding(11, settingsInset).Gap(10)
				}
				box.Children(row)
			}
		})
		if footnote != "" {
			ui.Text(c, footnote).FontSize(11).TextColor(Muted).LineHeight(1.5).PaddingX(2)
		}
	})
}

// SettingsSeparator is the hairline between rows, inset from the leading
// edge as in macOS lists.
func SettingsSeparator(c *ui.Context) {
	ui.Box(c).FillWidth().Padding(0, 0, 0, settingsInset).Children(func() {
		ui.Box(c).FillWidth().Height(1).Background(Line)
	})
}

// SettingLabel is a setting's name with its explanation below.
func SettingLabel(c *ui.Context, title, help string) ui.Element {
	return ui.Column(c).MinWidth(0).Gap(2).Children(func() {
		ui.Text(c, title).FontSize(13).TextColor(Ink)
		if help != "" {
			ui.Text(c, help).FontSize(11).TextColor(Muted).LineHeight(1.45)
		}
	})
}

// SettingRow puts a setting's label on the leading side and its control on
// the trailing side.
func SettingRow(c *ui.Context, title, help string, control func()) {
	ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(16).Children(func() {
		SettingLabel(c, title, help).Grow(1)
		if control != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(control)
		}
	})
}

// SettingsHeader titles a section of a settings page, with an explanation
// and actions on the trailing side.
func SettingsHeader(c *ui.Context, title, subtitle string, actions func()) {
	ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Start).Gap(12).PaddingX(2).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(Ink)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12).TextColor(Muted).LineHeight(1.5)
			}
		})
		if actions != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(actions)
		}
	})
}

// SettingsCard is the plain rounded panel free-form settings content sits in.
func SettingsCard(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().MinWidth(0).Padding(14, settingsInset).Gap(12).Background(Panel).Border(1, Line).Radius(settingsRadius)
}

// SmallButton is the compact outlined button for secondary actions, like a
// macOS push button at the small control size.
func SmallButton(c *ui.Context, label string, disabled bool) ui.Element {
	b := Quiet(c).Height(26).PaddingX(10).Radius(6).Border(1, Border).Background(Panel).Label(label).Disabled(disabled)
	if b.Hovered() && !disabled {
		b.Background(Hover)
	}
	if disabled {
		b.TextColor(Faint)
	}
	return b.Children(func() { ui.Text(c, label).FontSize(12) })
}
