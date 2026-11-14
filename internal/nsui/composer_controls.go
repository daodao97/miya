package nsui

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

// The composer's controls keep the original window's look: quiet faces
// that fill only under the pointer, each opening a compact menu above it
// whose current choice carries a check, with an icon for what has one.

// controlRow lines up composer controls, tight as a toolbar.
func controlRow(nodes ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2, MinWidth: core.Pt(0)}, Children: nodes}
}

func displayWhen(on bool) core.Display {
	if on {
		return core.DisplayFlex
	}
	return core.DisplayNone
}

// noticeLine is a muted line that takes no room, nor its column's gap,
// while it is empty.
func noticeLine(cx *core.Scope, content func() string) core.Node {
	return core.View{StyleFunc: func() core.Style { return core.Style{Display: displayWhen(content() != "")} },
		Children: []core.Node{nswidgets.MutedText(cx, content)}}
}

// menuText is a line of text in tint over style.
func menuText(text func() string, style core.Style, tint func() core.Color, lines int) core.Node {
	return core.Text{ContentFunc: text, MaxLines: lines, StyleFunc: func() core.Style {
		s := style
		s.Color = tint()
		s.MinWidth = core.Pt(0)
		return s
	}}
}

// composerPopover is a composer control whose face opens items in a menu
// above it, aligned to the face's align edge.
func (d *desktopComposer) composerPopover(id, label string, align core.Align, width core.Points, face, items func(cx *core.Scope) []core.Node) core.Node {
	return core.Func(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		return base.Popover{TestID: "picker-" + id, Label: label, Placement: core.PlaceTop, Align: align, Offset: 6, Margin: 8,
			Open: func() bool { return d.menu.Get() == id },
			OnOpenChange: func(open bool, _ base.Reason) {
				if open {
					d.menu.Set(id)
				} else if d.menu.GetUntracked() == id {
					d.menu.Set("")
				}
			},
			Style:     core.Style{Group: "picker", CornerRadius: ui.RadiusControl, Disabled: &core.Style{Opacity: core.Alpha(0.45)}},
			StyleFunc: func() core.Style { return core.Style{FocusVisible: ui.FocusRing(cx, 2)} },
			Trigger: core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 5,
				Height: core.Pt(28), MinWidth: core.Pt(28), Padding: core.Symmetric(0, 7), CornerRadius: ui.RadiusControl, BorderCurve: core.BorderCurveContinuous},
				StyleFunc: func() core.Style {
					return core.Style{GroupStyle: &core.GroupStyle{Hover: &core.Style{Background: colors.Hover()}, Active: &core.Style{Background: colors.Pressed()}, Expanded: &core.Style{Background: colors.Hover()}}}
				}, Children: face(cx)},
			PopupStyle: core.Style{Padding: core.All(5), Gap: 1, Width: core.Pt(width)},
			PopupStyleFunc: func() core.Style {
				return ui.FinOf(cx).Menu().Over(core.Style{CornerRadius: ui.RadiusCard, BorderWidth: 1, Color: colors.Text()})
			},
			Children: items(cx)}
	})
}

// menuHeader names a composer menu.
func menuHeader(cx *core.Scope, text func() string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Text{ContentFunc: text, MaxLines: 1, StyleFunc: func() core.Style {
		return core.Style{FontSize: 11, FontWeight: core.FontWeightSemibold, Color: colors.Muted(), Padding: core.Edges{Top: 5, Right: 9, Bottom: 5, Left: 9}}
	}}
}

// menuItem is a choice in a composer menu: lead, body, and a check while
// it is the current one.
func menuItem(cx *core.Scope, testID string, label func() string, lead core.Node, body []core.Node, selected func() bool, press func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	var children []core.Node
	if lead != nil {
		children = append(children, lead)
	}
	children = append(children,
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 2}, Children: body},
		core.View{StyleFunc: func() core.Style { return core.Style{Display: displayWhen(selected())} }, Children: []core.Node{nswidgets.Icon("check", 14, colors.Muted)}})
	return core.View{TestID: testID, Role: core.RoleButton, LabelFunc: label, OnPress: func(core.PressEvent) { press() },
		Style:     core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(32), Padding: core.Symmetric(5, 9), CornerRadius: 6, BorderCurve: core.BorderCurveContinuous},
		StyleFunc: func() core.Style { return core.Style{Hover: &core.Style{Background: colors.Hover()}} },
		Children:  children}
}

// textPicker is a muted caption with a chevron, as the model and thinking
// controls show, choosing among choices.
func (d *desktopComposer) textPicker(id, label string, width core.Points, caption, current func() string, choices func() []model.ComposerChoice, pick func(string)) core.Node {
	choices = Live(choices)
	return d.composerPopover(id, label, core.AlignEnd, width, func(cx *core.Scope) []core.Node {
		colors := nswidgets.ColorsOf(cx)
		return []core.Node{menuText(caption, core.Style{FontSize: 12}, colors.Muted, 1), nswidgets.Icon("chevron-down", 10, colors.Faint)}
	}, func(cx *core.Scope) []core.Node {
		colors := nswidgets.ColorsOf(cx)
		return []core.Node{menuHeader(cx, func() string { return label }), core.Dynamic(func(cx *core.Scope) core.Node {
			var rows []core.Node
			for _, choice := range choices() {
				name, text := choice.Name, firstSettingsText(choice.Detail, choice.Name)
				rows = append(rows, menuItem(cx, "pick-"+id+"-"+name, func() string { return text }, nil,
					[]core.Node{menuText(func() string { return text }, core.Style{FontSize: 13}, colors.Ink, 1)},
					func() bool { return current() == name }, func() { pick(name); d.menu.Set("") }))
			}
			return core.Group(rows)
		})}
	})
}

// agentPicker shows the agent's mark and name; it is fixed once the
// conversation has its first message, and then drops its chevron.
func (d *desktopComposer) agentPicker() core.Node {
	agent := Live(d.host.CurrentAgent)
	fixed := Live(func() bool { return d.host.Current().ID != 0 })
	badge := func(cx *core.Scope, id func() string) core.Node {
		colors := nswidgets.ColorsOf(cx)
		return core.Text{ContentFunc: func() string { return d.host.AgentSpeedBadge(id()) }, MaxLines: 1, StyleFunc: func() core.Style {
			return core.Style{Display: displayWhen(d.host.AgentSpeedBadge(id()) != ""), FontSize: 10, FontWeight: core.FontWeightSemibold, Color: colors.Muted()}
		}}
	}
	popover := d.composerPopover("agent", tr("Agents"), core.AlignStart, 220, func(cx *core.Scope) []core.Node {
		colors := nswidgets.ColorsOf(cx)
		return []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node { return nswidgets.AgentIcon(agent(), 16) }),
			menuText(func() string { return model.AgentName(agent()) }, core.Style{FontSize: 13}, colors.Ink, 1),
			badge(cx, agent),
			core.View{StyleFunc: func() core.Style { return core.Style{Display: displayWhen(!fixed())} }, Children: []core.Node{nswidgets.Icon("chevron-down", 10, colors.Faint)}},
		}
	}, func(cx *core.Scope) []core.Node {
		colors := nswidgets.ColorsOf(cx)
		return []core.Node{menuHeader(cx, func() string { return tr("Agents") }), core.Dynamic(func(cx *core.Scope) core.Node {
			var rows []core.Node
			for _, id := range Live(d.host.EnabledAgents)() {
				id := id
				name := func() string { return model.AgentName(id) }
				rows = append(rows, menuItem(cx, "pick-agent-"+id, name, nswidgets.AgentIcon(id, 16),
					[]core.Node{core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
						menuText(name, core.Style{FontSize: 13}, colors.Ink, 1), badge(cx, func() string { return id }),
					}}},
					func() bool { return agent() == id }, func() {
						if d.host.Current().ID == 0 && d.host.State().SetAgent != nil {
							d.host.State().SetAgent(id)
						}
						d.menu.Set("")
					}))
			}
			return core.Group(rows)
		})}
	})
	// The disabled wrapper also blocks the popover's trigger.
	return core.View{Disabled: fixed, Children: []core.Node{popover}}
}

type permissionChoice struct {
	value, title, detail, icon string
	warn                       bool
}

func permissionChoices() []permissionChoice {
	return []permissionChoice{
		{"on-request", tr("Ask when needed"), tr("Always ask before editing external files or accessing the internet"), "hand-stop", false},
		{"on-failure", tr("Automatic approval"), tr("Request approval only for detected risky actions"), "shield-check", false},
		{"never", tr("Full access"), tr("Unrestricted access to the internet and any file on your computer"), "shield-exclamation", true},
	}
}

func permissionChoiceFor(policy string) permissionChoice {
	for _, choice := range permissionChoices() {
		if choice.value == policy {
			return choice
		}
	}
	return permissionChoices()[0]
}

// fullAccessTint marks unrestricted access, the original's warning orange.
var fullAccessTint = core.RGB(0xdb6b3d)

// permissionsPicker is the policy's icon alone, orange for full access,
// naming the policy in its tooltip; its menu explains each one.
func (d *desktopComposer) permissionsPicker() core.Node {
	policy := Live(d.host.CurrentPermissionPolicy)
	popover := d.composerPopover("permissions", tr("Permissions"), core.AlignEnd, 340, func(cx *core.Scope) []core.Node {
		colors := nswidgets.ColorsOf(cx)
		return []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			choice := permissionChoiceFor(policy())
			tint := colors.Muted
			if choice.warn {
				tint = func() core.Color { return fullAccessTint }
			}
			return nswidgets.Icon(choice.icon, 15, tint)
		})}
	}, func(cx *core.Scope) []core.Node {
		colors := nswidgets.ColorsOf(cx)
		items := []core.Node{menuHeader(cx, Live(func() string { return d.host.CurrentAgentControls().Name() + tr(" permissions") }))}
		for _, choice := range permissionChoices() {
			choice := choice
			ink, tint := colors.Ink, colors.Muted
			if choice.warn {
				ink = func() core.Color { return fullAccessTint }
				tint = ink
			}
			items = append(items, menuItem(cx, "pick-permissions-"+choice.value, func() string { return choice.title },
				core.View{Style: core.Style{AlignSelf: core.AlignStart, Padding: core.Edges{Top: 2}}, Children: []core.Node{nswidgets.Icon(choice.icon, 18, tint)}},
				[]core.Node{
					menuText(func() string { return choice.title }, core.Style{FontSize: 13, FontWeight: core.FontWeightSemibold}, ink, 1),
					menuText(func() string { return choice.detail }, core.Style{FontSize: 11}, colors.Muted, 2),
				}, func() bool { return policy() == choice.value }, func() { d.host.SetCurrentPermissionPolicy(choice.value); d.menu.Set("") }))
		}
		return items
	})
	return ui.Tooltip{TextFunc: func() string { return permissionChoiceFor(policy()).title }, Trigger: popover}
}

// suggestions are the new conversation's starting ideas: quiet cards of
// equal width, their icon above their text.
func (d *desktopComposer) suggestions() core.Node {
	return core.Show{When: Live(func() bool { return d.host.Current().ID == 0 }), Then: func(cx *core.Scope) core.Node {
		colors := nswidgets.ColorsOf(cx)
		var cards []core.Node
		for _, s := range []struct{ id, icon, title, prompt string }{
			{"snake", "device-gamepad-2", "Create a classic snake game", "Create a classic snake game in this project."},
			{"bug", "bug", "Find and fix a bug in my code", "Find and fix a bug in my code. Explain the root cause and verify the fix."},
			{"pdf", "file-text", "Summarize this app in a PDF", "Summarize this app in a one-page PDF."},
		} {
			s := s
			title := func() string { return tr(s.title) }
			cards = append(cards, core.View{TestID: "suggestion-" + s.id, Role: core.RoleButton, LabelFunc: title,
				OnPress: func(core.PressEvent) { d.host.RememberDraft(tr(s.prompt)) },
				Style: core.Style{FlexGrow: 1, Width: core.Pt(0), MinWidth: core.Pt(0), Height: core.Pt(82), Padding: core.All(14), CornerRadius: 14,
					BorderCurve: core.BorderCurveContinuous, JustifyContent: core.JustifySpaceBetween, AlignItems: core.AlignStart},
				StyleFunc: func() core.Style {
					return core.Style{Background: colors.Subtle(), Hover: &core.Style{Background: colors.Hover()}, FocusVisible: ui.FocusRing(cx, 2)}
				},
				Children: []core.Node{nswidgets.Icon(s.icon, 20, colors.Faint), menuText(title, core.Style{FontSize: 12}, colors.Ink, 2)}})
		}
		return core.View{Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, Children: []core.Node{
			menuText(func() string { return tr("A few ideas to get started") }, core.Style{FontSize: 11, AlignSelf: core.AlignEnd}, colors.Muted, 1),
			core.View{Style: core.Style{Direction: core.Row, Gap: 10, MinWidth: core.Pt(0)}, Children: cards},
		}}
	}}
}
