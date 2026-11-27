package nsmobile

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
	"miya/internal/remote"
	"miya/internal/textutil"
)

// The drawer settles as iOS sidebars do: a slide that slows to a stop
// without bouncing, shorter for a drawer already part way there.
const drawerSlide = 380 * time.Millisecond

var drawerCurve = core.Curve{X1: 0.32, Y1: 0.72, X2: 0, Y2: 1}

func drawerWidth(cx *core.Scope) core.Points { return min(320, cx.Env().Window().Width*0.84) }

// shell is the drawer beside the home and conversation pages, off screen to
// their left. A finger pulls it from the page's left edge, or the sidebar
// button opens it; the page slides right and rounds its corner under a
// scrim, which closes it.
func (m *App) shell(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	width := func() core.Points { return drawerWidth(cx) }
	// The drawer and the page move by native transforms the platform drives
	// on its main thread, after the finger or a timing, so a slide lays
	// nothing out again. The drawer stays built, hidden while closed, so
	// pulling it out builds nothing either.
	pos := m.drawerPos
	shift := core.NewMemo(cx, func() core.AnimatedNode {
		w := float64(width())
		return core.Interpolate(pos, core.Interpolation{Input: []float64{0, w}, Output: []float64{0, w}, Extrapolate: core.ExtrapolateClamp})
	})
	shade := core.NewMemo(cx, func() core.AnimatedNode {
		w := float64(width())
		return core.Interpolate(pos, core.Interpolation{Input: []float64{0, w}, Colors: []core.Color{ui.Black(0), ui.Black(0.45)}, Extrapolate: core.ExtrapolateClamp})
	})
	slide := core.NewMemo(cx, func() core.AnimatedNode {
		w := float64(width())
		return core.Interpolate(pos, core.Interpolation{Input: []float64{0, w}, Output: []float64{-w, 0}, Extrapolate: core.ExtrapolateClamp})
	})
	committed := core.Untracked(cx, m.drawerOpen.Get)
	cx.Effect(func() {
		open := m.drawerOpen.Get()
		cx.Untrack(func() {
			if open {
				m.drawerActive.Set(true)
			}
			m.settleDrawer(cx, open, width())
			if open == committed {
				return
			}
			committed = open
			cx.PlayHaptic(core.HapticLight)
			if open {
				if !m.drawerExpanded && m.ExpandedProject == "" {
					m.ExpandedProject = m.SelectedProject().Path
				}
				m.drawerExpanded = true
				if m.Connected {
					m.Action(remote.Request{Op: "quota"})
				}
			} else {
				m.drawerSearching.Set(false)
				m.search.Set("")
			}
			m.change()
		})
	})
	pan := m.drawerPan(cx, width)
	// Only a mostly horizontal drag moves the drawer; a vertical one stays
	// with the lists it starts on.
	activation := core.PanActivation{ActiveOffsetX: 12, FailOffsetY: 14}
	follow := core.AnimatedXY{X: pos}
	active := m.drawerActive.Get
	return core.View{TestID: "mobile-shell", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100), Overflow: core.Hidden},
		StyleFunc: func() core.Style { return core.Style{Background: colors.Canvas()} },
		Children: []core.Node{
			core.View{TestID: "mobile-main", StyleFunc: func() core.Style {
				var radius core.Points
				if active() {
					radius = 32
				}
				return core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100), CornerRadius: radius, Overflow: core.Hidden, Background: colors.Canvas(),
					Animated: &core.AnimatedStyle{TranslateX: shift.Get()}}
			}, Children: []core.Node{
				m.page(cx),
				// The left edge pulls the drawer out, below the header and above
				// the composer so their buttons keep their presses. It stays
				// mounted while it drags, under the scrim that follows.
				core.Show{When: m.drawerPage, Then: func(cx *core.Scope) core.Node {
					return core.View{TestID: "drawer-edge", OnPan: pan, PanActivation: activation, PanValues: follow,
						Style: core.Style{Position: core.Absolute, Top: core.Pt(64), Left: core.Pt(0), Bottom: core.Pt(140), Width: core.Pt(24)}}
				}},
				core.Show{When: active, Then: func(cx *core.Scope) core.Node {
					return core.View{TestID: "drawer-scrim", Role: core.RoleButton, Label: tr("Close sidebar"), OnPan: pan, PanActivation: activation, PanValues: follow,
						OnPress: func(core.PressEvent) { m.drawerOpen.Set(false) },
						StyleFunc: func() core.Style {
							return core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), Background: ui.Black(0),
								Animated: &core.AnimatedStyle{Background: shade.Get()}}
						}}
				}},
			}},
			// The drawer lies over the page's left part and slides off to its
			// left; drawn after the page, an open drawer takes its presses.
			core.View{TestID: "mobile-drawer", OnPan: pan, PanActivation: activation, PanValues: follow, Disabled: func() bool { return !active() },
				StyleFunc: func() core.Style {
					w := width()
					st := core.Style{Position: core.Absolute, Top: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0), Width: core.Pt(w),
						Padding: core.Edges{Top: 12, Left: 24, Right: 24, Bottom: 16}, Gap: 12, Background: colors.Canvas(),
						Animated: &core.AnimatedStyle{TranslateX: slide.Get()}}
					if !active() {
						st.Visibility = core.VisibilityHidden
					}
					return st
				},
				Children: m.drawerContent(cx)},
			m.voiceStatus(cx),
		}}
}

// drawerPage reports whether the page under the drawer is one it belongs
// to: home or a conversation, not the remote desktop or a file browser.
func (m *App) drawerPage() bool {
	m.revision.Get()
	return m.Link != "" && m.filesURL.Get() == "" && (m.Path == "/" || m.Path == "/thread" || m.Path == "")
}

func (m *App) toggleDrawer() { m.drawerOpen.Set(!m.drawerOpen.GetUntracked()) }

// drawerPan settles the drawer open or closed by where a drag left it and
// how fast it was thrown. The platform moves it during the drag.
func (m *App) drawerPan(cx *core.Scope, width func() core.Points) func(core.PanEvent) {
	return func(e core.PanEvent) {
		w := float64(width())
		switch e.Phase {
		case core.GestureBegan:
			m.stopDrawer()
			m.drawerActive.Set(true)
		case core.GestureChanged:
		default:
			at := min(w, max(0, float64(e.Values.X)))
			m.drawerAt = at
			m.drawerPos.Set(at)
			open := at > w/2
			if e.Velocity.X > 300 {
				open = true
			} else if e.Velocity.X < -300 {
				open = false
			}
			if open == m.drawerOpen.GetUntracked() {
				m.settleDrawer(cx, open, core.Points(w))
			} else {
				m.drawerOpen.Set(open)
			}
		}
	}
}

// settleDrawer slides the drawer open or closed on the platform; a closed
// one hides once it is there.
func (m *App) settleDrawer(cx *core.Scope, open bool, w core.Points) {
	m.stopDrawer()
	to := 0.0
	if open {
		to = float64(w)
	}
	from := m.drawerAt
	m.drawerAt = to
	done := func(finished bool) {
		if finished && !m.drawerOpen.GetUntracked() {
			m.drawerActive.Set(false)
		}
	}
	if from == to || w <= 0 || cx.Env().ReduceMotion() {
		m.drawerPos.Set(to)
		done(true)
		return
	}
	d := max(time.Duration(float64(drawerSlide)*math.Abs(to-from)/float64(w)), 120*time.Millisecond)
	m.drawerRun = cx.StartAnimation(core.Timing(m.drawerPos, to, core.TimingConfig{Duration: d, Easing: core.BezierEasing(drawerCurve)}), done)
}

func (m *App) stopDrawer() {
	if m.drawerRun != nil {
		m.drawerRun.Stop()
		m.drawerRun = nil
	}
}

// desktopLabel is the connection's state, else the desktop's name.
func (m *App) desktopLabel() string {
	if line := m.ConnectionLine(); line != "" {
		return line
	}
	return textutil.FirstNonEmpty(strings.TrimSuffix(m.DesktopName(), ".local"), tr("Connected"))
}

// drawerContent is the drawer: the desktop and a search, the destinations,
// the activity and projects, then New thread and Settings.
func (m *App) drawerContent(cx *core.Scope) []core.Node {
	colors := nswidgets.ColorsOf(cx)
	go2 := func(route string) func() {
		return func() { m.drawerOpen.Set(false); m.pages.Open(route) }
	}
	return []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, FlexShrink: 0}, Children: []core.Node{
			core.View{TestID: "desktop-switcher", Role: core.RoleButton, Label: tr("Switch desktop"), OnPress: func(core.PressEvent) { m.act(go2("desktops"))() },
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 4}, Children: []core.Node{
					core.Text{Content: "Miya", MaxLines: 1, Style: core.Style{FontSize: 22, FontWeight: 600}},
					core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
						core.Text{ContentFunc: live(m, m.desktopLabel), MaxLines: 1, StyleFunc: func() core.Style {
							return core.Style{FontSize: 12, Color: colors.Muted(), FlexShrink: 1, MinWidth: core.Pt(0)}
						}},
						nswidgets.Icon("chevron-down", 11, colors.Muted),
					}},
				}},
			core.Show{When: live(m, func() bool { return m.appUpdate.url != "" }), Then: func(cx *core.Scope) core.Node {
				return m.circle(cx, "drawer-update", fmt.Sprintf(tr("Available version: %s"), m.appUpdate.version), "chevron-down", func() {
					cx.OpenURL(m.appUpdate.url, nil)
				})
			}},
			m.circle(cx, "drawer-search", tr("Search conversations"), "search", func() {
				m.drawerSearching.Set(!m.drawerSearching.GetUntracked())
				m.search.Set("")
			}),
		}},
		core.Show{When: m.drawerSearching.Get, Then: func(cx *core.Scope) core.Node {
			return base.Input{TestID: "home-search", Label: tr("Search conversations"), Placeholder: tr("Search conversation titles"), Value: m.search.Get, OnChange: m.search.Set,
				NoAutoCorrect: true, AutoCapitalize: core.AutoCapitalizeNone,
				Style:     core.Style{MinHeight: core.Pt(44), Padding: core.Symmetric(10, 12), CornerRadius: 12, FontSize: 16, FlexShrink: 0},
				StyleFunc: func() core.Style { return core.Style{Background: colors.Bubble()} }}
		}},
		m.errorText(cx),
		// The lists scroll only up and down: their content is held to the
		// drawer's width, so a long title cannot widen it into a sideways scroll.
		core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100)}, Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pct(100), MinWidth: core.Pt(0), Overflow: core.Hidden}, Children: []core.Node{m.drawerList(cx)}},
		}},
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, FlexShrink: 0}, Children: []core.Node{
			core.View{TestID: "drawer-new-thread", Role: core.RoleButton, Label: tr("New thread"),
				OnPress: func(core.PressEvent) {
					m.act(func() {
						// As the desktop's New thread: back to the page a
						// conversation starts from, in the selected project.
						m.drawerOpen.Set(false)
						m.StartConversation(m.SelectedProject().Path, m.Agent())
					})()
				},
				StyleFunc: func() core.Style {
					return core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(48), Padding: core.Symmetric(0, 20), CornerRadius: 24,
						FlexShrink: 1, MinWidth: core.Pt(0), Background: colors.Ink()}
				},
				Children: []core.Node{
					nswidgets.Icon("edit", 21, colors.Canvas),
					core.Text{Content: tr("New thread"), MaxLines: 1, StyleFunc: func() core.Style {
						return core.Style{FontSize: 16, FontWeight: 600, Color: colors.Canvas(), FlexShrink: 1, MinWidth: core.Pt(0)}
					}},
				}},
			core.View{Style: core.Style{FlexGrow: 1}},
			m.round(cx, "settings", tr("Settings"), "settings", 48, go2("settings")),
		}},
	}
}

// drawerList is rebuilt only when what it shows changes, not on every
// streamed message, so a press on it is not lost to a rebuild.
func (m *App) drawerList(cx *core.Scope) core.Node {
	key := core.NewMemo(cx, live(m, m.drawerKey))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		key.Get()
		return core.Untracked(cx, func() core.Node { return m.drawerRows(cx) })
	})
}

func (m *App) drawerKey() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v\x00%s\x00%s\x00%d\x00%s\x00", m.drawerSearching.Get(), m.search.Get(), m.ExpandedProject, m.Open, m.Path)
	thread := func(t model.Thread) {
		fmt.Fprintf(&b, "%d:%s:%s:%s:%v:%v:%v:%d\x01", t.ID, t.Title, model.ThreadAgent(t), t.Automation, t.Busy, m.CompletionUnread(t), m.ThreadWaiting(t), len(m.ApprovalsFor(t.ServerID)))
	}
	for _, p := range m.State.Projects {
		fmt.Fprintf(&b, "\x02%s:%s:%v:%v\x02", p.Path, p.Name, m.ThreadListExpanded[p.Path], m.automationRunsShown[p.Path])
		for _, t := range p.Threads {
			thread(t)
		}
	}
	waiting, running, completed := m.SidebarActivityThreads()
	b.WriteString("\x03")
	for _, t := range waiting {
		thread(t)
	}
	b.WriteString("\x03")
	for _, t := range running {
		thread(t)
	}
	b.WriteString("\x03")
	for _, t := range completed {
		thread(t)
	}
	return b.String()
}

func (m *App) drawerRows(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	muted := func(s string, size core.Points) core.Node {
		return core.Text{Content: s, StyleFunc: func() core.Style { return core.Style{FontSize: size, Color: colors.Muted()} }}
	}
	var nodes []core.Node
	if m.drawerSearching.GetUntracked() {
		matches := appmodel.MatchingThreadRefs(m.State.Projects, 0, strings.TrimSpace(m.search.GetUntracked()))
		if len(matches) == 0 {
			nodes = append(nodes, core.View{Style: core.Style{Padding: core.Symmetric(12, 0)}, Children: []core.Node{muted(tr("No matching conversations"), 14)}})
		}
		for _, match := range matches {
			nodes = append(nodes, m.drawerThread(cx, match.Thread, "thread-"))
		}
		return core.View{Style: core.Style{MinWidth: core.Pt(0)}, Children: nodes}
	}
	nav := func(id, icon, label, route string) core.Node {
		return core.View{TestID: id, Role: core.RoleButton, Label: label, OnPress: func(core.PressEvent) {
			m.act(func() { m.drawerOpen.Set(false); m.pages.Open(route) })()
		}, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, MinHeight: core.Pt(48)}, Children: []core.Node{
			nswidgets.Icon(icon, 21, colors.Ink),
			core.Text{Content: label, MaxLines: 1, Style: core.Style{FontSize: 17, FlexShrink: 1, MinWidth: core.Pt(0)}},
		}}
	}
	nodes = append(nodes,
		nav("automations", "clock", tr("Automations"), "automations"),
		nav("quota", "pi", tr("Usage"), "quota"),
		core.View{Style: core.Style{Height: core.Pt(1), Margin: core.Edges{Top: 16, Bottom: 12}}, StyleFunc: func() core.Style { return core.Style{Background: colors.Line()} }},
	)
	waiting, running, completed := m.SidebarActivityThreads()
	for _, section := range []struct {
		label   string
		threads []model.Thread
	}{{tr("Waiting for you"), waiting}, {tr("In progress"), running}, {tr("Completed"), completed}} {
		if len(section.threads) == 0 {
			continue
		}
		nodes = append(nodes, core.View{Style: core.Style{Margin: core.Edges{Bottom: 6}}, Children: []core.Node{muted(section.label, 13)}})
		for _, t := range section.threads {
			nodes = append(nodes, m.drawerThread(cx, t, "activity-"))
		}
		nodes = append(nodes, core.View{Style: core.Style{Height: core.Pt(16)}})
	}
	nodes = append(nodes, core.View{Style: core.Style{Margin: core.Edges{Bottom: 6}}, Children: []core.Node{muted(tr("Projects"), 13)}})
	for _, p := range m.State.Projects {
		nodes = append(nodes, m.drawerProject(cx, p))
	}
	return core.View{Style: core.Style{MinWidth: core.Pt(0)}, Children: nodes}
}

// drawerProject is a project's row, which unfolds its conversations.
func (m *App) drawerProject(cx *core.Scope, p model.ProjectSnapshot) core.Node {
	colors := nswidgets.ColorsOf(cx)
	path := p.Path
	expanded := m.ExpandedProject == path
	busy := false
	for _, t := range p.Threads {
		busy = busy || t.Busy
	}
	var trail core.Node
	switch {
	case busy && !expanded:
		trail = ui.Spinner{Size: 12, Label: tr("Project activity: ") + p.Name}
	case expanded:
		trail = nswidgets.Icon("chevron-down", 12, colors.Muted)
	default:
		trail = nswidgets.Icon("chevron-right", 12, colors.Muted)
	}
	nodes := []core.Node{core.View{TestID: "project-" + path, Role: core.RoleButton, Label: tr("Project ") + p.Name,
		OnPress: func(core.PressEvent) {
			m.act(func() {
				m.Selected = path
				if m.ExpandedProject == path {
					m.ExpandedProject = ""
				} else {
					m.ExpandedProject = path
				}
			})()
		},
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(44), CornerRadius: 10}, Children: []core.Node{
			nswidgets.Icon("folder", 18, colors.Muted),
			core.Text{Content: p.Name, MaxLines: 1, Style: core.Style{FontSize: 15, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
			trail,
		}}}
	if expanded {
		if len(p.Threads) == 0 {
			nodes = append(nodes, core.View{Style: core.Style{Padding: core.Symmetric(8, 0)}, Children: []core.Node{
				core.Text{Content: tr("No conversations yet"), StyleFunc: func() core.Style { return core.Style{FontSize: 14, Color: colors.Muted()} }},
			}})
		}
		threads, runs := msgfmt.SplitAutomationThreads(p.Threads, func(t model.Thread) bool {
			return t.Busy || m.CompletionUnread(t) || m.ThreadWaiting(t) || t.ID == m.Open && m.Path == "/thread"
		})
		more := m.ThreadListExpanded[path]
		for _, t := range msgfmt.DisplayedThreads(threads, more) {
			nodes = append(nodes, m.drawerThread(cx, t, "thread-"))
		}
		if len(threads) > msgfmt.CollapsedThreadCount {
			label := tr("Expand")
			if more {
				label = tr("Collapse")
			}
			nodes = append(nodes, core.View{TestID: "more-" + path, Role: core.RoleButton, Label: label,
				OnPress: func(core.PressEvent) {
					m.act(func() {
						if m.ThreadListExpanded == nil {
							m.ThreadListExpanded = map[string]bool{}
						}
						m.ThreadListExpanded[path] = !more
					})()
				},
				Style: core.Style{MinHeight: core.Pt(44), JustifyContent: core.JustifyCenter}, Children: []core.Node{
					core.Text{Content: label, StyleFunc: func() core.Style { return core.Style{FontSize: 15, Color: colors.Muted()} }},
				}})
		}
		if len(runs) > 0 {
			shown := m.automationRunsShown[path]
			label := fmt.Sprintf(tr("Automation runs (%d)"), len(runs))
			icon := "chevron-right"
			if shown {
				icon = "chevron-down"
			}
			nodes = append(nodes, core.View{TestID: "automation-runs-" + path, Role: core.RoleButton, Label: label,
				OnPress: func(core.PressEvent) { m.act(func() { m.automationRunsShown[path] = !shown })() },
				Style:   core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinHeight: core.Pt(44), Gap: 8, Padding: core.Symmetric(0, 8)},
				Children: []core.Node{nswidgets.Icon(icon, 12, colors.Muted), core.Text{Content: label, StyleFunc: func() core.Style {
					return core.Style{FontSize: 15, Color: colors.Muted()}
				}}},
			})
			if shown {
				for _, t := range runs {
					nodes = append(nodes, m.drawerThread(cx, t, "thread-"))
				}
			}
		}
	}
	return core.View{Style: core.Style{MinWidth: core.Pt(0)}, Children: nodes}
}

// drawerThread is a conversation's row: the open one is highlighted, and
// its status marks a pending approval, a running turn or an unread reply.
func (m *App) drawerThread(cx *core.Scope, t model.Thread, prefix string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	title := msgfmt.NativeText(textutil.FirstNonEmpty(t.Title, tr("Untitled conversation")))
	open := t.ID == m.Open && m.Path == "/thread"
	agent := model.ThreadAgent(t)
	lead := nswidgets.AgentIcon(agent, 18)
	if t.Automation != "" {
		tint := nswidgets.AgentTint(agent)
		lead = nswidgets.Icon("clock", 18, func() core.Color { return tint })
	}
	children := []core.Node{
		lead,
		core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: 16, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
	}
	switch {
	case len(m.ApprovalsFor(t.ServerID)) > 0:
		children = append(children, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5, FlexShrink: 0}, Children: []core.Node{
			dot(7, colors.WarnInk),
			core.Text{Content: tr("Pending approval"), MaxLines: 1, StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.WarnInk()} }},
		}})
	case m.ThreadWaiting(t):
		children = append(children, dot(7, colors.WarnInk))
	case t.Busy:
		children = append(children, ui.Spinner{Size: 13})
	case m.CompletionUnread(t):
		children = append(children, dot(8, colors.Success))
	}
	return core.View{TestID: prefix + strconv.Itoa(t.ID), Role: core.RoleButton, Label: title,
		OnPress: func(core.PressEvent) { m.act(func() { m.drawerOpen.Set(false); m.OpenThread(t) })() },
		StyleFunc: func() core.Style {
			s := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(48), Padding: core.Symmetric(0, 8), CornerRadius: 10}
			if open {
				s.Background = colors.Bubble()
			}
			return s
		}, Children: children}
}

// homeHeader is the home page's bar: the sidebar, Miya and its desktop's
// state, and More.
func (m *App) homeHeader(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinHeight: core.Pt(52), Padding: core.Symmetric(0, 12), Gap: 12, FlexShrink: 0}, Children: []core.Node{
		m.iconAction(cx, "open-sidebar", tr("Open sidebar"), "layout-sidebar", m.toggleDrawer),
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), AlignItems: core.AlignCenter, Gap: 3}, Children: []core.Node{
			core.Text{TestID: "mobile-title", Content: "Miya", MaxLines: 1, Style: core.Style{FontSize: 16, FontWeight: 600}},
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{
				dot(6, live(m, func() core.Color {
					if m.ConnectionLine() == "" {
						return colors.Success()
					}
					return colors.Muted()
				})),
				core.Text{ContentFunc: live(m, m.desktopLabel), MaxLines: 1, StyleFunc: func() core.Style {
					return core.Style{FontSize: 11, Color: colors.Muted(), FlexShrink: 1, MinWidth: core.Pt(0)}
				}},
			}},
		}},
		m.iconAction(cx, "conversation-actions", tr("More"), "dots", func() { m.pages.Open("actions") }),
	}}
}

// threadHeader is a conversation's bar: the sidebar, its title over its
// agent and project (or the connection while it is down), and More.
func (m *App) threadHeader(cx *core.Scope) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinHeight: core.Pt(64), Padding: core.Symmetric(0, 12), Gap: 8, FlexShrink: 0}, Children: []core.Node{
		m.circle(cx, "open-sidebar", tr("Open sidebar"), "layout-sidebar", m.toggleDrawer),
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), AlignItems: core.AlignCenter, Gap: 2}, Children: []core.Node{
			core.Text{TestID: "mobile-title", MaxLines: 1, Style: core.Style{FontSize: 16, FontWeight: 600}, ContentFunc: live(m, func() string {
				return msgfmt.NativeText(textutil.FirstNonEmpty(m.Title, tr("New conversation")))
			})},
			core.Text{TestID: "mobile-subtitle", MaxLines: 1, ContentFunc: live(m, m.threadSubtitle), StyleFunc: func() core.Style {
				return core.Style{FontSize: 12, Color: colors.Muted()}
			}},
		}},
		m.circle(cx, "conversation-actions", tr("More"), "dots", func() { m.pages.Open("actions") }),
	}}
}

func (m *App) threadSubtitle() string {
	if line := m.ConnectionLine(); line != "" {
		return line
	}
	agent := model.ThreadAgent(m.Viewer.Current())
	if m.Open == 0 || agent == "" {
		agent = m.Agent()
	}
	parts := []string{m.agentName(agent)}
	if name := m.projectName(m.ComposerProject()); name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, " · ")
}

func (m *App) projectName(path string) string {
	for _, p := range m.State.Projects {
		if p.Path == path {
			return p.Name
		}
	}
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}
