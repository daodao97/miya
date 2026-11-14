package nsui

import (
	"fmt"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/ui/chat"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/interaction"
	"miya/internal/nschat"
	"miya/internal/nswidgets"
	"miya/locales"
)

func tr(english string) string { return locales.T(english) }

// root is the window: the conversations in a sidebar beside the selected
// one, on an inset card, as Agio's window is.
// sidebarFooter goes at the sidebar's end, as the workspace selector.
func root(cx *core.Scope, c Host, sidebarFooter ...core.Node) core.Node {
	// page is the main area: "chat" or "automations". Choosing a
	// conversation always goes back to chat.
	usage := usageFor(cx, c)
	page := core.NewSignal(cx, "chat")
	// expanded is the project whose conversations the sidebar lists.
	expanded := core.NewSignal(cx, c.State().Cwd)
	reads := &readTracker{c: c}
	c = chatHost{Host: c, page: page, expanded: expanded, reads: reads}
	st := c.State()
	threads := Live(func() []model.Thread {
		if st.Threads == nil {
			return nil
		}
		return st.Threads()
	})
	current := Live(c.Current)
	refProjects := Live(c.ThreadRefProjects)
	shortcutsOpen := core.NewSignal(cx, false)
	open := core.NewSignal(cx, true)
	width := core.NewSignal(cx, core.Points(260))
	// panelOpen shows the right panel; see panel.go.
	panelOpen := core.NewSignal(cx, false)
	panelWidth := core.NewSignal(cx, core.Points(460))
	cx.OnKey("mod+alt+b", func() { panelOpen.Update(func(b bool) bool { return !b }) })
	// settingsOpen shows the settings dialog; see settings.go.
	settingsOpen := core.NewSignal(cx, false)
	paletteOpen := core.NewSignal(cx, false)
	usage.openSettings = func(category string) { usage.category.Set(category); settingsOpen.Set(true) }
	cx.Cleanup(func() { usage.openSettings = nil })
	cmd := commands{
		openPalette:   func() { paletteOpen.Set(true) },
		shortcuts:     func() { shortcutsOpen.Set(true) },
		newThread:     c.NewThread,
		openSettings:  func() { settingsOpen.Set(true) },
		toggleSidebar: func() { open.Update(func(b bool) bool { return !b }) },
		stop: func() {
			if c.Current().Busy {
				c.Stop()
			}
		},
		step: func(step int) {
			all := core.Untracked(cx, threads)
			id := stepThread(all, c.Current().ID, step)
			for _, t := range all {
				if t.ID == id {
					c.SelectThread(t)
				}
			}
		},
	}
	bindCommands(cx, cmd)
	colors := nswidgets.ColorsOf(cx)
	convState := chat.NewConversationState(cx)
	conversation := nschat.View{TestID: "conversation", Thread: current, State: convState,
		OnRetry:  func(r appmodel.RetryRequest) { _ = c.RetryResponse(r) },
		OnReload: func() { c.SelectThread(c.Current()) },
		OnStop:   c.Stop,
		Requests: pendingRequests(cx, c, Live(func() []interaction.Request {
			if st.Approvals == nil {
				return nil
			}
			return st.Approvals()
		})),
	}
	// The conversation counts as seen while it shows in a focused window.
	cx.Effect(func() {
		current()
		showing, atLatest := page.Get() == "chat", convState.AtLatest()
		cx.Untrack(func() { reads.markVisible(showing, atLatest) })
	})
	projectNode, openProject := projectDialog(cx, c, expanded)
	desktopComposer := newDesktopComposer(cx, c)
	desktopComposer.active = func() bool { return page.Get() == "chat" && !settingsOpen.Get() && !paletteOpen.Get() }
	desktopComposer.wire(&conversation)
	conversationKey := core.NewMemo(cx, Live(func() string {
		return fmt.Sprintf("%s:%s:%d", c.State().ReferenceSource, c.State().Cwd, c.Current().ID)
	}))
	cx.Effect(func() {
		conversationKey.Get()
		cx.Untrack(func() {
			desktopComposer.complete.close()
			desktopComposer.menu.Set("")
			desktopComposer.diffOpen.Set(false)
		})
	})
	conversationNode := core.Dynamic(func(cx *core.Scope) core.Node {
		conversationKey.Get()
		return core.Func(func(cx *core.Scope) core.Node { return conversation })
	})

	return ui.SidebarShell{TestID: "window", Variant: ui.SidebarShellInset,
		Open: open.Get, OnOpenChange: open.Set, ToggleLabel: tr("Toggle sidebar"),
		Width: width.Get, OnWidthChange: width.Set, ResizeLabel: tr("Resize sidebar"),
		DefaultWidth: 260, MinWidth: 220, MaxWidth: 420,
		Right: []core.Node{filePanel(cx, c, panelOpen)}, RightOpen: panelOpen.Get, OnRightOpenChange: panelOpen.Set,
		RightToggleLabel: tr("Show / Hide right panel"), RightResizeLabel: tr("Resize right panel"),
		RightWidth: panelWidth.Get, OnRightWidthChange: panelWidth.Set, RightDefaultWidth: 460, RightMinWidth: 320, RightMaxWidth: 960,
		Sidebar: []core.Node{sidebar(cx, c, shellNav{page: page, expanded: expanded, settings: cmd.openSettings, project: openProject, usage: func() { usage.openSettings("usage") }, footer: sidebarFooter})},
		Header: []core.Node{core.Text{TestID: "current-title", MaxLines: 1,
			ContentFunc: func() string {
				if page.Get() == "automations" {
					return tr("Automations")
				}
				return threadTitle(current())
			},
			Style:     core.Style{FontSize: 13, FontWeight: ui.Semibold, FlexShrink: 1, MinWidth: core.Pt(0)},
			StyleFunc: func() core.Style { return core.Style{Color: colors.Ink()} }}},
		Content: []core.Node{
			// The conversation stays built while hidden so it keeps its scroll;
			// automations exist only while shown, so they poll only then.
			core.View{StyleFunc: func() core.Style {
				return ui.FillRest.Merge(core.Style{Display: displayIf(page.Get() == "chat")})
			}, Children: []core.Node{conversationNode}},
			core.Show{When: func() bool { return page.Get() == "automations" }, Then: func(cx *core.Scope) core.Node { return automationsPage(cx, c) }},
			commandPalette(cx, paletteOpen, func() []paletteEntry {
				return paletteEntries(cmd, current(), threadChoices(refProjects()), c.SelectThread)
			}), settingsDialog(cx, c, settingsOpen), projectNode, shortcutsDialog(cx, shortcutsOpen)},
	}
}

func threadTitle(t model.Thread) string {
	if t.Title != "" {
		return t.Title
	}
	return tr("New conversation")
}

// chatHost returns to the conversation whenever one is chosen or started.
// A conversation in another project opens that project first, and choosing
// one marks it read once its history shows.
type chatHost struct {
	Host
	page     *core.Signal[string]
	expanded *core.Signal[string]
	reads    *readTracker
}

func (h chatHost) SelectThread(t model.Thread) {
	h.page.Set("chat")
	st := h.State()
	if path := h.ThreadWorkspace(t.ID); path != "" && path != st.Cwd {
		if err := h.ChangeWorkspace(path); err != nil {
			if st.SetStatus != nil {
				st.SetStatus("Project selection failed: " + err.Error())
			}
			return
		}
		h.expanded.Set(path)
	}
	h.Host.SelectThread(t)
	h.reads.request(t)
}

func (h chatHost) NewThread() {
	h.page.Set("chat")
	h.Host.NewThread()
}
