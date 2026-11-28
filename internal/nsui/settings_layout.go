package nsui

import (
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

func desktopSettingsPage(cx *core.Scope, category, query *core.Signal[string], categories []ui.SettingsCategory, close func(), dialog bool) core.Node {
	colors := nswidgets.ColorsOf(cx)
	selected := func() string {
		id := category.Get()
		for _, pane := range categories {
			if pane.ID == id {
				return id
			}
		}
		return categories[0].ID
	}
	choose := func(id string) {
		category.Set(id)
		query.Set("")
	}

	var tabs []core.Node
	for _, pane := range categories {
		active := func() bool { return selected() == pane.ID && strings.TrimSpace(query.Get()) == "" }
		tint := func() core.Color {
			if active() {
				return colors.Ink()
			}
			return colors.Muted()
		}
		tabs = append(tabs, base.Button{
			Role: core.RoleTab, Label: pane.Title, TestID: "settings-nav-" + pane.ID, Selected: active,
			OnPress: func(core.PressEvent) { choose(pane.ID) },
			Style: core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 4, Padding: core.Symmetric(6, 9),
				MinWidth: core.Pt(55), MinHeight: core.Pt(49), FlexShrink: 0, CornerRadius: 8, BorderCurve: core.BorderCurveContinuous},
			StyleFunc: func() core.Style {
				style := core.Style{Hover: &core.Style{Background: colors.Hover()}, FocusVisible: ui.FocusRing(cx, -2)}
				if active() {
					style.Background = colors.Chosen()
				}
				return style
			},
			Children: []core.Node{ui.Icon(pane.Icon, 19, tint), ui.Text(pane.Title, 11, ui.Medium, tint)},
		})
	}
	toolbar := core.View{
		Style: core.Style{FlexShrink: 0, Padding: core.Edges{Top: 10, Right: 16, Bottom: 10, Left: 16}, Borders: core.Edges{Bottom: 1}},
		StyleFunc: func() core.Style {
			return core.Style{Background: colors.Panel(), BorderColors: core.EdgeColors{Bottom: colors.Line()}}
		},
		Children: []core.Node{core.View{Role: core.RoleTabList, Label: tr("Settings"), Style: core.Style{
			Direction: core.Row, Wrap: core.Wrap, JustifyContent: core.JustifyCenter, Gap: 4,
		}, Children: tabs}},
	}
	scroll := &core.Ref{}
	lastPage := core.NewMemo(cx, func() string {
		if strings.TrimSpace(query.Get()) != "" {
			return ""
		}
		return selected()
	})
	cx.Effect(func() {
		lastPage.Get()
		scroll.ScrollTo(core.Point{}, false)
	})
	return core.View{TestID: "settings", Style: ui.FillRest, StyleFunc: func() core.Style { return core.Style{Background: colors.Canvas()} },
		Children: []core.Node{toolbar, core.Scroll{Ref: scroll, TestID: "settings-scroll", Style: ui.FillRest, Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(720), AlignSelf: core.AlignCenter, Padding: core.All(20), Gap: 16},
				Children: []core.Node{
					core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 10}, Children: []core.Node{
						core.Text{Role: core.RoleHeader, ContentFunc: func() string {
							id := lastPage.Get()
							for _, pane := range categories {
								if pane.ID == id {
									return pane.Title
								}
							}
							return tr("Search settings")
						}, Style: core.Style{FontSize: 23, FontWeight: ui.Semibold, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(120)}},
						ui.Input{TestID: "settings-search", Label: tr("Search settings"), Placeholder: tr("Search settings"),
							Value: query.Get, OnChange: query.Set, Style: core.Style{Width: core.Pt(180), MaxWidth: core.Pct(100)}},
						core.Show{When: func() bool { return dialog }, Then: func(*core.Scope) core.Node {
							return ui.IconButton{Icon: nswidgets.IconOf("x"), Label: tr("Close"), OnPress: close, TestID: "settings-close"}
						}},
					}},
					core.Dynamic(func(cx *core.Scope) core.Node {
						match := strings.TrimSpace(query.Get())
						return settingsGroups(cx, categories, lastPage.Get(), match)
					}),
				}},
		}}}}
}

func settingsGroups(cx *core.Scope, categories []ui.SettingsCategory, selected, query string) core.Node {
	var panes []core.Node
	matches := map[ui.SettingsResult]bool{}
	if query != "" {
		for _, result := range ui.SearchSettings(categories, query) {
			matches[result] = true
		}
	}
	for i, category := range categories {
		if query == "" && category.ID != selected {
			continue
		}
		var groups []core.Node
		for j, group := range category.Groups {
			var rows, fields []core.Node
			flushFields := func() {
				if len(fields) != 0 {
					rows = append(rows, ui.FormSection{Children: fields})
					fields = nil
				}
			}
			for k, item := range group.Items {
				if query != "" && !matches[ui.SettingsResult{Category: i, Group: j, Item: k}] {
					continue
				}
				id := "settings-" + category.ID + "-" + item.ID
				if item.Content != nil {
					flushFields()
					rows = append(rows, core.View{TestID: id, Children: []core.Node{item.Content}})
				} else if item.Title == "" && item.Control == nil {
					flushFields()
					rows = append(rows, core.View{TestID: id, Style: core.Style{Padding: core.Symmetric(0, 12)}, Children: []core.Node{
						settingsText(cx, func() string { return item.Description }),
					}})
				} else {
					fields = append(fields, ui.FormRow{TestID: id, Title: item.Title, Detail: item.Description, Leading: item.Leading, Control: item.Control})
				}
			}
			flushFields()
			if len(rows) == 0 {
				continue
			}
			var controls []core.Node
			if group.Title != "" && group.Title != category.Title {
				title := settingsSectionTitle(group.Title)
				title.Style.FlexGrow, title.Style.FlexShrink, title.Style.MinWidth = 1, 1, core.Pt(0)
				controls = append(controls, title)
			}
			if query == "" {
				controls = append(controls, group.Actions...)
			}
			if len(controls) != 0 {
				groups = append(groups, settingsActions(controls...))
			}
			groups = append(groups, core.View{Style: core.Style{Gap: 8}, Children: rows})
		}
		if len(groups) == 0 {
			continue
		}
		var heading []core.Node
		if query != "" {
			heading = append(heading, settingsSectionTitle(category.Title))
		}
		if category.Description != "" {
			heading = append(heading, settingsText(cx, func() string { return category.Description }))
		}
		panes = append(panes, core.View{Style: core.Style{Gap: 14}, Children: append(heading, groups...)})
	}
	if len(panes) == 0 {
		return settingsText(cx, func() string { return tr("No matching settings.") })
	}
	return core.View{Style: core.Style{Gap: 20}, Children: panes}
}

func settingsSectionTitle(title string) core.Text {
	return core.Text{Content: title, Role: core.RoleHeader, Style: core.Style{FontSize: 13, FontWeight: ui.Semibold}}
}

func settingsFormActions(nodes ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, JustifyContent: core.JustifyEnd, AlignItems: core.AlignCenter, Gap: 8}, Children: nodes}
}

func settingsField(label string, input core.Node) core.Node {
	return core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
		core.Text{Content: label, Style: core.Style{FontSize: 12, FontWeight: ui.Medium}}, input,
	}}
}

func settingsInlineField(label string, input ui.Input) core.Node {
	title := core.Text{Content: label, Style: core.Style{Width: core.Pt(135), MinWidth: core.Pt(0), FontSize: 12, FontWeight: ui.Medium}}
	input.Style = input.Style.Merge(core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)})
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10}, Children: []core.Node{title, input}}
}
