package nsui

import (
	"slices"
	"strconv"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

type settingsSelect struct {
	TestID, Label string
	Options       []ui.Option[string]
	Value         func() string
	OnChange      func(string)
	Disabled      func() bool
}

func (s settingsSelect) Render(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	open := core.NewSignal(cx, false)
	items := make([]base.SelectItem, len(s.Options))
	var textWidth core.Points
	for i, option := range s.Options {
		items[i] = base.SelectItem{Value: option.Value, Title: option.Label}
		textWidth = max(textWidth, core.Points(cx.MeasureText(option.Label, core.Font{Size: 13}).Width))
	}
	selected := core.NewMemo(cx, func() int {
		value := s.Value()
		return slices.IndexFunc(items, func(item base.SelectItem) bool { return item.Value == value })
	})
	value := func() *string {
		if i := selected.Get(); i >= 0 {
			return &items[i].Value
		}
		return nil
	}

	// Keep ordinary values compact and reserve extra room for translated and
	// agent-specific labels. The menu can grow farther than the closed field.
	width := min(196, max(144, textWidth+40))
	menuWidth := min(304, max(width, textWidth+48))
	title := core.Text{
		ContentFunc: func() string {
			if i := selected.Get(); i >= 0 {
				return items[i].Title
			}
			return tr("Choose")
		},
		MaxLines: 1, Style: core.Style{FontSize: 12, FontWeight: ui.Medium, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
		StyleFunc: func() core.Style {
			color := colors.Ink()
			if selected.Get() < 0 {
				color = colors.Muted()
			}
			return core.Style{Color: color}
		},
	}

	face := core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Height: core.Pt(27), Gap: 8,
			Padding: core.Symmetric(0, 9), CornerRadius: 7, BorderCurve: core.BorderCurveContinuous, BorderWidth: 1},
		StyleFunc: func() core.Style {
			border := colors.Edge()
			if open.Get() {
				border = colors.Link()
			}
			return core.Style{Background: colors.Panel(), BorderColor: border,
				GroupStyle: &core.GroupStyle{
					Hover:  &core.Style{Background: colors.Hover()},
					Active: &core.Style{Background: colors.Chosen()},
				}}
		},
		Children: []core.Node{title, ui.Icon(lucide.ChevronsUpDown, 14, colors.Muted)},
	}

	return base.Select{
		TestID: s.TestID, Label: s.Label, Disabled: s.Disabled,
		Items: func() []base.SelectItem { return items }, Value: value,
		OnChange: func(v *string) {
			if v != nil {
				s.OnChange(*v)
			}
		},
		Open: open.Get, OnOpenChange: func(on bool, _ base.Reason) { open.Set(on) },
		Align: core.AlignEnd, Offset: 4, Margin: 8,
		RootStyle: core.Style{Width: core.Pt(width), AlignSelf: core.AlignStart},
		Style:     core.Style{Group: "button", CornerRadius: 7, Disabled: ui.Dimmed()},
		StyleFunc: func() core.Style {
			return core.Style{FocusVisible: ui.FocusRing(cx, 2)}
		},
		Trigger: face,
		PopupStyle: core.Style{Width: core.Pt(menuWidth), Padding: core.All(4), CornerRadius: 10,
			BorderCurve: core.BorderCurveContinuous, BorderWidth: 1, ShadowOffset: core.Point{Y: 4}, ShadowRadius: 8},
		PopupStyleFunc: func() core.Style {
			return core.Style{Background: colors.Panel(), BorderColor: colors.Edge(), ShadowColor: colors.Shadow()}
		},
		ListStyle: core.Style{MaxHeight: core.Pt(260)},
		ItemStyle: core.Style{MinHeight: core.Pt(27), CornerRadius: 6, BorderCurve: core.BorderCurveContinuous},
		ItemStyleFunc: func() core.Style {
			return core.Style{
				Selected: &core.Style{Background: ui.Mix(colors.Panel(), colors.Link(), .10)},
				Focus:    &core.Style{Background: colors.Hover()},
			}
		},
		RenderItem: func(item base.SelectEntry) core.Node {
			return core.View{
				TestID: s.TestID + "-" + strconv.Itoa(item.Index),
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 7, MinHeight: core.Pt(27),
					Padding: core.Symmetric(4, 7), MinWidth: core.Pt(0)},
				Children: []core.Node{
					core.View{Style: core.Style{Width: core.Pt(13), FlexShrink: 0}, Children: []core.Node{
						core.Show{When: func() bool { return selected.Get() == item.Index }, Then: func(*core.Scope) core.Node {
							return ui.Icon(lucide.Check, 13, colors.Link)
						}},
					}},
					core.Text{Content: item.Title, Style: core.Style{FontSize: 13, LineHeight: 17, FlexShrink: 1, MinWidth: core.Pt(0)},
						StyleFunc: func() core.Style { return core.Style{Color: colors.Ink()} }},
				},
			}
		},
	}
}
