package nsui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

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
func root(cx *core.Scope, c Host) core.Node {
	// page is the main area: "chat" or "automations". Choosing a
	// conversation always goes back to chat.
	page := core.NewSignal(cx, "chat")
	c = chatHost{Host: c, page: page}
	st := c.State()
	threads := Live(func() []model.Thread {
		if st.Threads == nil {
			return nil
		}
		return st.Threads()
	})
	current := Live(c.Current)
	open := core.NewSignal(cx, true)
	width := core.NewSignal(cx, core.Points(260))
	// settingsOpen shows the settings dialog; see settings.go.
	settingsOpen := core.NewSignal(cx, false)
	paletteOpen := core.NewSignal(cx, false)
	cmd := commands{
		openPalette:   func() { paletteOpen.Set(true) },
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
			id := stepThread(all, c.Current().ID, step, time.Now())
			for _, t := range all {
				if t.ID == id {
					c.SelectThread(t)
				}
			}
		},
	}
	bindCommands(cx, cmd)
	colors := nswidgets.ColorsOf(cx)
	conversation := nschat.View{TestID: "conversation", Thread: current,
		OnRetry:  func(r appmodel.RetryRequest) { _ = c.RetryResponse(r) },
		OnReload: func() { c.SelectThread(c.Current()) },
		Draft: Live(func() string {
			if st.Draft == nil {
				return ""
			}
			return st.Draft()
		}),
		OnDraftChange: c.RememberDraft,
		// Sending while a turn runs steers it, as on MyGo.
		OnSend: func() { c.SendDraft(c.Current().Busy) },
		OnStop: c.Stop,
		Requests: pendingRequests(cx, c, Live(func() []interaction.Request {
			if st.Approvals == nil {
				return nil
			}
			return st.Approvals()
		})),
	}
	return ui.SidebarShell{TestID: "window", Variant: ui.SidebarShellInset,
		Open: open.Get, OnOpenChange: open.Set, ToggleLabel: tr("Toggle sidebar"),
		Width: width.Get, OnWidthChange: width.Set, ResizeLabel: tr("Resize sidebar"),
		DefaultWidth: 260, MinWidth: 220, MaxWidth: 420,
		Sidebar: []core.Node{core.View{Style: ui.FillRest.Merge(core.Style{Gap: 2}), Children: []core.Node{
			core.View{Style: core.Style{Padding: core.Symmetric(0, 12), FlexShrink: 0}, Children: []core.Node{
				ui.SidebarButton{Text: tr("New conversation"), Icon: nswidgets.IconOf("edit"), TestID: "new-thread", OnPress: c.NewThread},
				ui.SidebarButton{Text: tr("Automations"), Icon: nswidgets.IconOf("clock"), TestID: "open-automations", OnPress: func() { page.Set("automations") }},
			}},
			threadSidebar(cx, threads, current, c),
		}}},
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
			}, Children: []core.Node{conversation}},
			core.Show{When: func() bool { return page.Get() == "automations" }, Then: func(cx *core.Scope) core.Node { return automationsPage(cx, c) }},
			commandPalette(cx, paletteOpen, func() []paletteEntry {
				return paletteEntries(cmd, current(), threads(), c.SelectThread, time.Now())
			}), settingsDialog(cx, c, settingsOpen)},
	}
}

// period groups conversations that are not running by local calendar day:
// one falls into the first period it is old enough for, the last unbounded.
type period struct {
	key, title string
	days       int
}

var periods = []period{
	{"today", "Today", 0},
	{"yesterday", "Yesterday", 1},
	{"week", "Previous 7 days", 7},
	{"month", "Previous 30 days", 30},
	{"older", "Older", -1},
}

func periodOf(now, t time.Time) int {
	y, mo, d := now.Date()
	midnight := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
	for i, p := range periods {
		if p.days < 0 || !t.Before(midnight.AddDate(0, 0, -p.days)) {
			return i
		}
	}
	return len(periods) - 1
}

type threadGroup struct {
	key, title string
	threads    []model.Thread
}

// lastActive is when t was last active.
func lastActive(t model.Thread) time.Time {
	for _, ms := range []int64{t.LastActivityAt, t.CompletedAtMs, t.StartedAtMs} {
		if ms > 0 {
			return time.UnixMilli(ms)
		}
	}
	return time.Time{}
}

// threadGroups are the running conversations, then the others by period,
// most recently active first, without empty groups.
func threadGroups(threads []model.Thread, now time.Time) []threadGroup {
	list := slices.Clone(threads)
	slices.SortStableFunc(list, func(a, b model.Thread) int { return lastActive(b).Compare(lastActive(a)) })
	groups := []threadGroup{{key: "running", title: "Running"}}
	for _, p := range periods {
		groups = append(groups, threadGroup{key: p.key, title: p.title})
	}
	for _, t := range list {
		i := 0
		if !t.Busy {
			i = 1 + periodOf(now, lastActive(t))
		}
		groups[i].threads = append(groups[i].threads, t)
	}
	return slices.DeleteFunc(groups, func(g threadGroup) bool { return len(g.threads) == 0 })
}

// threadSidebar lists the conversations in collapsible groups with their
// counts. Its rows are built again only when what they show changes.
func threadSidebar(cx *core.Scope, threads func() []model.Thread, current func() model.Thread, c Host) core.Node {
	closed, folds := map[string]bool{}, core.NewSignal(cx, 0)
	key := core.NewMemo(cx, func() string {
		var b strings.Builder
		for _, g := range threadGroups(threads(), time.Now()) {
			fmt.Fprint(&b, g.key, ":")
			for _, t := range g.threads {
				fmt.Fprint(&b, t.ID, "\x00", t.Title, "\x00", t.Project, ",")
			}
		}
		return b.String()
	})
	sections := func() []ui.SidebarSection {
		key.Get()
		var sections []ui.SidebarSection
		cx.Untrack(func() {
			colors := ui.ColorsOf(cx)
			groups := threadGroups(threads(), time.Now())
			if len(groups) == 0 {
				sections = []ui.SidebarSection{{Note: ui.Text(tr("No conversations yet"), 13, ui.Regular, colors.Muted)}}
				return
			}
			for _, g := range groups {
				items := make([]ui.SidebarItem, 0, len(g.threads))
				for _, t := range g.threads {
					id := strconv.Itoa(t.ID)
					items = append(items, ui.SidebarItem{ID: id, Text: threadTitle(t), TestID: "thread-" + id})
				}
				sections = append(sections, ui.SidebarSection{Title: tr(g.title), Collapsible: true, Items: items, TestID: "threads-" + g.key,
					Trailing: ui.Text(strconv.Itoa(len(g.threads)), 11, ui.Regular, colors.Muted),
					Open:     func() bool { folds.Get(); return !closed[g.key] },
					OnOpenChange: func(open bool) {
						closed[g.key] = !open
						folds.Update(func(n int) int { return n + 1 })
					}})
			}
		})
		return sections
	}
	return ui.Sidebar{SectionsFunc: sections, TestID: "threads",
		Selected: func() string { return strconv.Itoa(current().ID) },
		OnSelect: func(id string) {
			n, err := strconv.Atoi(id)
			if err != nil {
				return
			}
			for _, t := range threads() {
				if t.ID == n {
					c.SelectThread(t)
					return
				}
			}
		}}
}

func threadTitle(t model.Thread) string {
	if t.Title != "" {
		return t.Title
	}
	return tr("New conversation")
}

// chatHost returns to the conversation whenever one is chosen or started.
type chatHost struct {
	Host
	page *core.Signal[string]
}

func (h chatHost) SelectThread(t model.Thread) {
	h.page.Set("chat")
	h.Host.SelectThread(t)
}

func (h chatHost) NewThread() {
	h.page.Set("chat")
	h.Host.NewThread()
}
