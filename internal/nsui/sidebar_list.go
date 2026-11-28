package nsui

import (
	"slices"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
)

const (
	projectChildIndent core.Points = 16
	threadIconSize     core.Points = 14
	threadIconGap      core.Points = 8
	threadTitleIndent              = threadIconSize + threadIconGap
)

type sidebarItem struct {
	id, testID, text, icon, agent                       string
	nested, automation, loading, busy, unread, expanded bool
	indent                                              core.Points
}

type sidebarSection struct {
	id, title string
	items     []sidebarItem
	open      *core.Signal[bool]
}

type sidebarList struct {
	sections   func() []sidebarSection
	selected   func() string
	choose     func(string)
	addProject func()
	menu       func(sidebarItem) ([]core.Node, func())
}

func (s sidebarList) Render(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	focused, taking := core.NewSignal(cx, false), core.NewSignal(cx, 0)
	take := func() { taking.Update(func(n int) int { return n + 1 }) }
	scroll, content := &core.Ref{}, &core.Ref{}
	refs := map[string]*core.Ref{}
	var offset core.Points
	walk := func(step int, edge bool) {
		var ids []string
		for _, sec := range core.Untracked(cx, s.sections) {
			if sec.open != nil && !sec.open.GetUntracked() {
				continue
			}
			for _, item := range sec.items {
				ids = append(ids, item.id)
			}
		}
		index := slices.Index(ids, core.Untracked(cx, s.selected))
		at := index + step
		switch {
		case edge && step < 0:
			at = len(ids) - 1
		case edge || index < 0:
			at = 0
		}
		if at < 0 || at >= len(ids) {
			return
		}
		id := ids[at]
		s.choose(id)
		ref := refs[id]
		if ref == nil {
			return
		}
		r, ok := ref.Bounds()
		c, laidOut := content.Bounds()
		view, visible := scroll.Bounds()
		if ok && laidOut && visible {
			top := r.Origin.Y - c.Origin.Y
			bottom := top + r.Size.Height
			switch {
			case top < offset:
				scroll.ScrollTo(core.Point{Y: top}, true)
			case bottom > offset+view.Size.Height:
				scroll.ScrollTo(core.Point{Y: bottom - view.Size.Height}, true)
			}
		}
	}
	sections := core.Each(s.sections, func(sec sidebarSection) string { return sec.id },
		func(cx *core.Scope, get func() sidebarSection, _ func() int) core.Node {
			sec := core.Untracked(cx, get)
			items := core.Each(func() []sidebarItem { return get().items }, func(item sidebarItem) string { return item.id },
				func(cx *core.Scope, item func() sidebarItem, _ func() int) core.Node {
					first := core.Untracked(cx, item)
					ref := &core.Ref{}
					refs[first.id] = ref
					cx.Cleanup(func() {
						if refs[first.id] == ref {
							delete(refs, first.id)
						}
					})
					selected := func() bool { return s.selected() == first.id }
					onFocus := func() bool { return selected() && focused.Get() }
					menu, newThread := s.menu(first)
					return sidebarRow(cx, ref, first, item, selected, onFocus, func() { take(); s.choose(first.id) }, menu, newThread)
				})
			if sec.title == "" {
				return core.View{Style: core.Style{Gap: 2}, Children: []core.Node{items}}
			}
			label := ui.Text(sec.title, 11, ui.Bold, colors.Muted)
			label.Role, label.MaxLines = core.RoleHeader, 1
			label.Style.Padding = core.Symmetric(3, 8)
			label.Style.FlexGrow, label.Style.FlexShrink = 1, 1
			if sec.open != nil {
				const group = "activity-heading"
				label.Role = core.RoleNone
				symbol := core.Dynamic(func(*core.Scope) core.Node {
					if sec.open.Get() {
						return nswidgets.Icon("chevron-down", 12, colors.Faint)
					}
					return nswidgets.Icon("chevron-right", 12, colors.Faint)
				})
				return base.Collapsible{TestID: sec.id, Open: sec.open.Get, Reveal: func() bool { return true },
					Children: []core.Node{base.Button{Label: sec.title, Expanded: sec.open.Get,
						OnPress:   func(core.PressEvent) { take(); sec.open.Update(func(open bool) bool { return !open }) },
						Style:     core.Style{Group: group, Direction: core.Row, AlignItems: core.AlignCenter, Padding: core.Edges{Right: 8}, CornerRadius: 8},
						StyleFunc: func() core.Style { return core.Style{FocusVisible: ui.FocusRing(cx, -2)} },
						Children: []core.Node{label, core.View{Style: core.Style{Opacity: core.Alpha(0),
							GroupStyle: &core.GroupStyle{Name: group, Hover: &core.Style{Opacity: core.Alpha(1)}, Focus: &core.Style{Opacity: core.Alpha(1)}}},
							Children: []core.Node{symbol}}},
					}},
					Content: core.View{Style: core.Style{Padding: core.Edges{Top: 2}, Gap: 2}, Children: []core.Node{items}}}
			}
			header := []core.Node{label}
			if sec.id == "projects" {
				header = append(header, ui.IconButton{Icon: nswidgets.IconOf("folder-plus"), Label: tr("Select project"), Size: 22, IconSize: 14, OnPress: s.addProject})
			}
			return core.View{TestID: sec.id, Style: core.Style{Gap: 2}, Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Padding: core.Edges{Right: 4}}, Children: header}, items,
			}}
		})
	bindings := core.Show{When: func() bool { return taking.Get() > 0 }, Then: func(cx *core.Scope) core.Node {
		repeat := core.KeyOptions{When: core.KeyNotEditing, Repeat: true}
		cx.OnKeyWith("up", repeat, func() { walk(-1, false) })
		cx.OnKeyWith("down", repeat, func() { walk(1, false) })
		cx.OnKeyWith("home", repeat, func() { walk(1, true) })
		cx.OnKeyWith("end", repeat, func() { walk(-1, true) })
		return nil
	}}
	return core.Scroll{Ref: scroll, TestID: "threads", Style: ui.FillRest, OnScroll: func(e core.ScrollEvent) { offset = e.Offset.Y },
		Children: []core.Node{core.View{Ref: content, OnPress: func(core.PressEvent) { take() },
			OnFocus: func() { focused.Set(true); take() }, OnBlur: func() { focused.Set(false); taking.Set(0) },
			Style: core.Style{Padding: core.Edges{Right: 10, Bottom: 12, Left: 10}, Gap: 10}, Children: []core.Node{sections, bindings}}}}
}

func sidebarRow(cx *core.Scope, ref *core.Ref, first sidebarItem, item func() sidebarItem, selected, focused func() bool, choose func(), menu []core.Node, newThread func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	project := newThread != nil
	title := func() string { return item().text }
	var lead core.Node
	switch {
	case first.agent != "":
		mark := core.NewMemo(cx, func() string {
			if i := item(); !i.automation {
				return i.agent
			}
			return ""
		})
		lead = core.Dynamic(func(*core.Scope) core.Node {
			if name := mark.Get(); name != "" {
				return nswidgets.AgentIcon(name, threadIconSize)
			}
			return nswidgets.Icon("clock", threadIconSize, func() core.Color { return nswidgets.AgentTint(item().agent) })
		})
	case project:
		lead = nswidgets.Icon("folder", 16, colors.Faint)
	case first.icon != "":
		lead = nswidgets.Icon(first.icon, 16, ui.ColorsOf(cx).Accent)
	}
	style := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: threadIconGap, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0),
		Padding: core.Edges{Left: first.indent}}
	if first.nested {
		style.Padding.Left += projectChildIndent
	}
	label := core.Text{ContentFunc: title, MaxLines: 1, Style: core.Style{FontSize: 13, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}}
	if lead == nil {
		label.Style.FontSize = 12
		label.StyleFunc = func() core.Style { return core.Style{Color: colors.Muted()} }
	}
	showingActions := func() bool { return false }
	const actionSize, actionGap, actionInset core.Points = 24, 2, 5
	row := core.View{Ref: ref, Role: core.RoleButton, LabelFunc: title, Selected: selected, NoFocus: true, TestID: first.testID,
		OnPress: func(core.PressEvent) { choose() },
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinHeight: core.Pt(32), Padding: core.Symmetric(0, 8),
			CornerRadius: 8, Cursor: core.CursorDefault},
		StyleFunc: func() core.Style {
			s := core.Style{Selected: &core.Style{Background: ui.Alpha(colors.Ink(), 0.1)}, FocusVisible: ui.FocusRing(cx, -2)}
			switch {
			case showingActions() && project:
				s.Padding.Right = actionInset + 2*(actionSize+actionGap)
			case project:
				s.Padding.Right = actionInset + actionSize + actionGap
			}
			return s
		}, PartStates: func() core.PartStates { return core.PartStates{Focused: focused()} },
		Children: []core.Node{core.View{Style: style, Children: []core.Node{lead, label}}}}
	if project || strings.HasPrefix(first.id, "r:") {
		row.Expanded = func() bool { return item().expanded }
	}
	if menu == nil {
		return row
	}

	hovered, menuOpen := core.NewSignal(cx, false), core.NewSignal(cx, false)
	showingActions = func() bool { return hovered.Get() || menuOpen.Get() || focused() }
	menuLabel := tr("Conversation actions")
	contextMenu := menu
	if project {
		menuLabel = tr("Project actions")
		contextMenu = append([]core.Node{ui.MenuItem{Text: tr("New thread"), Icon: nswidgets.IconOf("edit"), OnSelect: newThread}, ui.MenuSeparator{}}, menu...)
	}
	actions := []core.Node{ui.Menu{Label: menuLabel, TestID: "actions-" + first.testID, Open: menuOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { menuOpen.Set(open) }, Placement: core.PlaceBottom, Align: core.AlignEnd,
		Trigger: ui.Button{Variant: ui.ButtonGhost, Style: core.Style{Width: core.Pt(actionSize), Height: core.Pt(actionSize), CornerRadius: 6}},
		Face: core.View{Style: core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Width: core.Pct(100), Height: core.Pct(100)},
			Children: []core.Node{nswidgets.Icon("dots", 16, colors.Muted)}}, Children: menu}}
	if project {
		actions = append(actions, ui.IconButton{Icon: nswidgets.IconOf("edit"), Label: tr("New thread"), TestID: "new-" + first.testID,
			Size: actionSize, IconSize: 16, OnPress: newThread})
	}
	status := core.NewMemo(cx, func() string {
		switch i := item(); {
		case i.loading || i.busy:
			return "busy"
		case i.unread:
			return "unread"
		case project && i.expanded:
			return "chevron-down"
		case project:
			return "chevron-right"
		}
		return ""
	})
	trail := core.Dynamic(func(*core.Scope) core.Node {
		switch state := status.Get(); state {
		case "busy":
			if project {
				return core.View{LabelFunc: func() string { return tr("Project activity: ") + title() }, Children: []core.Node{ui.Spinner{Size: 13}}}
			}
			return ui.Spinner{Size: 13, TestID: "spinner-" + first.testID, LabelFunc: func() string {
				if item().loading {
					return tr("Loading conversation…")
				}
				return tr("Working…")
			}}
		case "unread":
			return core.View{LabelFunc: func() string { return tr("Unread: ") + title() }, StyleFunc: func() core.Style {
				return core.Style{Width: core.Pt(8), Height: core.Pt(8), CornerRadius: 4, Background: colors.Added()}
			}}
		case "":
			return nil
		default:
			return nswidgets.Icon(state, 12, colors.Faint)
		}
	})
	return ui.ContextMenu{Children: contextMenu, Trigger: core.View{
		OnMouseEnter: func(core.MouseEvent) { hovered.Set(true) }, OnMouseLeave: func(core.MouseEvent) { hovered.Set(false) },
		Children: []core.Node{row, core.View{Style: core.Style{Position: core.Absolute, Right: core.Pt(actionInset), Top: core.Pt(4), ZIndex: 1},
			StyleFunc: func() core.Style {
				style := core.Style{Display: displayIf(showingActions() || status.Get() != "")}
				if !project {
					// Cover the title with the same fill as the row behind it.
					style.Padding.Left = actionGap
					style.Background = ui.ColorsOf(cx).Inset().Over(colors.Canvas())
					if selected() {
						style.Background = ui.Alpha(colors.Ink(), 0.1).Over(style.Background)
					}
				}
				return style
			}, Children: []core.Node{
				core.View{Style: core.Style{Height: core.Pt(actionSize), Width: core.Pt(actionSize), AlignItems: core.AlignCenter,
					JustifyContent: core.JustifyCenter, PointerEvents: core.PointerEventsNone},
					StyleFunc: func() core.Style { return core.Style{Display: displayIf(!showingActions())} }, Children: []core.Node{trail}},
				// Keep the trigger mounted and visible while its menu is open.
				core.View{Style: core.Style{Direction: core.Row, Gap: actionGap},
					StyleFunc: func() core.Style { return core.Style{Display: displayIf(showingActions())} }, Children: actions},
			},
		}},
	}}
}
