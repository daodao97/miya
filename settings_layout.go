package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// zhOr picks the Chinese or English text for the interface language.
func zhOr(zh bool, zhText, en string) string {
	if zh {
		return zhText
	}
	return en
}

// settingsGroup is a titled card of settings rows split by hairlines, with
// an optional note under it.
func settingsGroup(c *ui.Context, title, footnote string, rows ...func()) {
	ui.Column(c.Key("group:" + title)).FillWidth().MinWidth(0).Gap(8).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(muted).PaddingX(4)
		}
		ui.Column(c).FillWidth().MinWidth(0).Background(panel).Border(1, line).Radius(12).Clip().Children(func() {
			for i, row := range rows {
				if i > 0 {
					ui.Box(c.Key(fmt.Sprint("sep", i))).FillWidth().Height(1).Background(line)
				}
				ui.Column(c.Key(fmt.Sprint("row", i))).FillWidth().MinWidth(0).Padding(14, 16).Gap(10).Children(row)
			}
		})
		if footnote != "" {
			ui.Text(c, footnote).FontSize(11).TextColor(muted).LineHeight(1.5).PaddingX(4)
		}
	})
}

// settingLabel is a setting's name with its explanation below.
func settingLabel(c *ui.Context, title, help string) ui.Element {
	return ui.Column(c).MinWidth(0).Gap(3).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(500)
		if help != "" {
			ui.Text(c, help).FontSize(12).TextColor(muted).LineHeight(1.45)
		}
	})
}

// settingRow puts a setting's label on the left and its control on the right.
func settingRow(c *ui.Context, title, help string, control func()) {
	ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(16).Children(func() {
		settingLabel(c, title, help).Grow(1)
		if control != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(control)
		}
	})
}

// settingsHeader is a settings page's title and explanation, with actions
// on the right.
func settingsHeader(c *ui.Context, title, subtitle string, actions func()) {
	ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(16).FontWeight(600)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12).TextColor(muted).LineHeight(1.5)
			}
		})
		if actions != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(actions)
		}
	})
}

// settingsCard is the plain panel settings content sits in.
func settingsCard(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().MinWidth(0).Padding(16).Gap(12).Background(panel).Border(1, line).Radius(12)
}

// smallButton is the compact outlined button for secondary actions.
func smallButton(c *ui.Context, label string, disabled bool) ui.Element {
	b := quiet(c).Height(28).PaddingX(10).Border(1, line).Label(label).Disabled(disabled)
	if disabled {
		b.TextColor(muted).Background(ui.RGBA(0, 0, 0, 0))
	}
	return b.Children(func() { ui.Text(c, label).FontSize(12) })
}
