package nsui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
	projectmodel "miya/internal/project/model"
)

// shellNav is what the sidebar changes in the shell around it.
type shellNav struct {
	page     *core.Signal[string]
	expanded *core.Signal[string]
	settings func()
	project  func()
	footer   []core.Node
}

// sidebarData is what the sidebar shows, read from the controller at once.
type sidebarData struct {
	cwd, history       string
	projects           []projectmodel.Project
	threads            []model.Thread
	running, completed []model.Thread
	busy               map[string]bool
	unread             map[int]bool
}

func gatherSidebar(c Host) sidebarData {
	st := c.State()
	d := sidebarData{cwd: st.Cwd, busy: map[string]bool{}, unread: map[int]bool{}}
	if st.Projects != nil {
		d.projects = st.Projects()
	}
	if st.Threads != nil {
		d.threads = st.Threads()
	}
	if st.HistoryStatus != nil {
		d.history = st.HistoryStatus()
	}
	// Activity spans projects, in project order, the open folder first when
	// it is not a project, as daily chats are not.
	var snapshots []model.ProjectSnapshot
	cwdProject := false
	for _, p := range d.projects {
		threads := st.ProjectCache[p.Path]
		if p.Path == st.Cwd {
			threads, cwdProject = d.threads, true
		}
		snapshots = append(snapshots, model.ProjectSnapshot{Threads: threads})
		d.busy[p.Path] = c.ProjectBusy(p.Path)
	}
	if !cwdProject {
		snapshots = append([]model.ProjectSnapshot{{Threads: d.threads}}, snapshots...)
	}
	d.running, d.completed = msgfmt.SidebarActivityThreads(snapshots, c.DesktopThreadUnread)
	for _, list := range [][]model.Thread{d.threads, d.running, d.completed} {
		for _, t := range list {
			if !t.Busy && c.DesktopThreadUnread(t) {
				d.unread[t.ID] = true
			}
		}
	}
	return d
}

// key changes whenever anything the sidebar draws does.
func (d sidebarData) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%s\x01", d.cwd, d.history)
	for _, p := range d.projects {
		fmt.Fprintf(&b, "%s\x00%s\x00%v,", p.Path, p.Name, d.busy[p.Path])
	}
	for _, list := range [][]model.Thread{d.threads, d.running, d.completed} {
		b.WriteByte('\x01')
		for _, t := range list {
			fmt.Fprintf(&b, "%d\x00%s\x00%v%v%v\x00%s\x00%s\x00%d,", t.ID, t.Title, t.Busy, t.Loading, d.unread[t.ID], t.Agent, t.Automation, t.LastActivityAt)
		}
	}
	return b.String()
}

func (d sidebarData) find(id int) (model.Thread, bool) {
	for _, list := range [][]model.Thread{d.threads, d.running, d.completed} {
		for _, t := range list {
			if t.ID == id {
				return t, true
			}
		}
	}
	return model.Thread{}, false
}

// sidebar is the navigation: a new conversation and automations, activity
// across projects, the projects with the open one's conversations, then
// settings and the workspace at the foot. Its rows are built again only
// when what they show changes.
func sidebar(cx *core.Scope, c Host, nav shellNav) core.Node {
	colors := nswidgets.ColorsOf(cx)
	more, moreRev := map[string]bool{}, core.NewSignal(cx, 0)
	data := Live(func() string { return gatherSidebar(c).key() })
	key := core.NewMemo(cx, func() string {
		return nav.page.Get() + "\x02" + nav.expanded.Get() + "\x02" + strconv.Itoa(moreRev.Get()) + "\x02" + data()
	})
	var last sidebarData
	selectProject := func(p projectmodel.Project) {
		st := c.State()
		if p.Path == st.Cwd && nav.expanded.GetUntracked() == p.Path {
			nav.expanded.Set("")
			return
		}
		if err := c.ChangeWorkspace(p.Path); err != nil {
			if st.SetStatus != nil {
				st.SetStatus("Project selection failed: " + err.Error())
			}
			if st.SetHistoryStatus != nil {
				st.SetHistoryStatus("History unavailable: " + err.Error())
			}
			return
		}
		nav.expanded.Set(c.State().Cwd)
		nav.page.Set("chat")
	}
	newThreadIn := func(p projectmodel.Project) {
		if err := c.ChangeWorkspace(p.Path); err != nil {
			if st := c.State(); st.SetStatus != nil {
				st.SetStatus(err.Error())
			}
			return
		}
		nav.expanded.Set(c.State().Cwd)
		c.NewThread()
	}
	threadItem := func(prefix, testPrefix string, t model.Thread, unread, nested bool) ui.SidebarItem {
		id := strconv.Itoa(t.ID)
		agent := model.ThreadAgent(t)
		lead := nswidgets.AgentIcon(agent, 14)
		if t.Automation != "" {
			tint := nswidgets.AgentTint(agent)
			lead = nswidgets.Icon("clock", 14, func() core.Color { return tint })
		}
		var trail core.Node = core.Group(nil)
		switch {
		case t.Busy && !t.Loading:
			trail = ui.Spinner{Size: 13}
		case unread:
			trail = core.View{Label: tr("Unread: ") + t.Title, StyleFunc: func() core.Style {
				return core.Style{Width: core.Pt(8), Height: core.Pt(8), CornerRadius: 4, FlexShrink: 0, Background: colors.Added()}
			}}
		}
		var indent core.Points
		if nested {
			indent = 16
		}
		content := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, FlexGrow: 1, MinWidth: core.Pt(0), Padding: core.Edges{Left: indent}},
			Children: []core.Node{lead,
				core.Text{Content: threadTitle(t), MaxLines: 1, Style: core.Style{FontSize: 13, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
				trail}}
		return ui.SidebarItem{ID: prefix + id, Text: threadTitle(t), Content: content, TestID: testPrefix + id,
			Menu: []core.Node{
				ui.MenuItem{Text: tr("Archive conversation"), OnSelect: func() { c.ArchiveThread(t) }},
				ui.MenuItem{Text: tr("Delete conversation"), Variant: ui.MenuItemDanger, OnSelect: func() { c.DeleteThread(t) }},
			}}
	}
	projectItem := func(p projectmodel.Project, d sidebarData, open bool) ui.SidebarItem {
		var trail core.Node
		switch {
		case !open && d.busy[p.Path]:
			trail = core.View{Label: tr("Project activity: ") + p.Name, Children: []core.Node{ui.Spinner{Size: 13}}}
		case open:
			trail = nswidgets.Icon("chevron-down", 12, colors.Faint)
		default:
			trail = nswidgets.Icon("chevron-right", 12, colors.Faint)
		}
		path := p.Path
		return ui.SidebarItem{ID: "p:" + path, Text: p.Name, TestID: "project-" + path,
			Expanded: func() bool { return nav.expanded.Get() == path },
			Content: core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, FlexGrow: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
				nswidgets.Icon("folder", 16, colors.Faint),
				core.Text{Content: p.Name, MaxLines: 1, Style: core.Style{FontSize: 13, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
				trail,
			}},
			Menu: []core.Node{
				ui.MenuItem{Text: tr("New thread"), Icon: nswidgets.IconOf("edit"), OnSelect: func() { newThreadIn(p) }},
				ui.MenuSeparator{},
				// Archiving hides the project and archives its conversations
				// while keeping the Codex project trust configuration intact.
				ui.MenuItem{Text: tr("Archive project"), OnSelect: func() { c.ArchiveProject(path) }},
				ui.MenuItem{Text: tr("Delete project"), Variant: ui.MenuItemDanger, OnSelect: func() { c.DeleteProject(path) }},
			}}
	}
	// threadItems are the open project's conversations, newest first and
	// compact unless expanded, with the history's state when it has none.
	threadItems := func(d sidebarData, path string, nested bool) []ui.SidebarItem {
		var items []ui.SidebarItem
		if len(d.threads) == 0 && d.history != "" && (d.history == tr("Loading conversations…") ||
			strings.HasPrefix(d.history, "Unable to load") || strings.HasPrefix(d.history, "History")) {
			items = append(items, ui.SidebarItem{ID: "h:" + path, Text: d.history, TestID: "history-status"})
		}
		for _, t := range msgfmt.DisplayedThreads(d.threads, more[path]) {
			items = append(items, threadItem("t:", "thread-", t, d.unread[t.ID], nested))
		}
		if len(d.threads) > msgfmt.CollapsedThreadCount {
			label := tr("Expand")
			if more[path] {
				label = tr("Collapse")
			}
			items = append(items, ui.SidebarItem{ID: "m:" + path, Text: label, TestID: "more-threads"})
		}
		return items
	}
	sections := func() []ui.SidebarSection {
		key.Get()
		var sections []ui.SidebarSection
		cx.Untrack(func() {
			d := gatherSidebar(c)
			last = d
			expanded := nav.expanded.GetUntracked()
			sections = append(sections, ui.SidebarSection{Items: []ui.SidebarItem{
				{ID: "nav:new", Text: tr("New conversation"), Icon: nswidgets.IconOf("edit"), TestID: "new-thread"},
				{ID: "nav:automations", Text: tr("Automations"), Icon: nswidgets.IconOf("clock"), TestID: "open-automations"},
			}})
			for _, a := range []struct {
				key, title string
				threads    []model.Thread
			}{{"running", tr("In progress"), d.running}, {"completed", tr("Completed"), d.completed}} {
				if len(a.threads) == 0 {
					continue
				}
				var items []ui.SidebarItem
				for _, t := range a.threads {
					items = append(items, threadItem("a:", "activity-", t, d.unread[t.ID], false))
				}
				sections = append(sections, ui.SidebarSection{Title: a.title, Items: items, TestID: "activity-" + a.key})
			}
			cwdProject := false
			var projects []ui.SidebarItem
			for _, p := range d.projects {
				open := p.Path == expanded
				projects = append(projects, projectItem(p, d, open))
				if p.Path == d.cwd {
					cwdProject = true
				}
				if open && p.Path == d.cwd {
					projects = append(projects, threadItems(d, p.Path, true)...)
				}
			}
			// Conversations outside every project, as daily chats, list on
			// their own.
			if !cwdProject {
				if items := threadItems(d, d.cwd, false); len(items) > 0 {
					sections = append(sections, ui.SidebarSection{Title: tr("Conversations"), Items: items, TestID: "threads-open"})
				}
			}
			sections = append(sections, ui.SidebarSection{Title: tr("Projects"), Items: projects, TestID: "projects",
				Actions: ui.IconButton{Icon: nswidgets.IconOf("folder-plus"), Label: tr("Select project"), Size: 22, IconSize: 14, OnPress: nav.project}})
		})
		return sections
	}
	current := Live(func() int { return c.Current().ID })
	list := ui.Sidebar{SectionsFunc: sections, TestID: "threads",
		Selected: func() string {
			if nav.page.Get() == "automations" {
				return "nav:automations"
			}
			return "t:" + strconv.Itoa(current())
		},
		OnSelect: func(id string) {
			kind, rest, _ := strings.Cut(id, ":")
			switch kind {
			case "nav":
				if rest == "new" {
					c.NewThread()
				} else {
					nav.page.Set("automations")
				}
			case "t", "a":
				if n, err := strconv.Atoi(rest); err == nil {
					if t, ok := last.find(n); ok {
						c.SelectThread(t)
					}
				}
			case "p":
				for _, p := range last.projects {
					if p.Path == rest {
						selectProject(p)
					}
				}
			case "m":
				more[rest] = !more[rest]
				moreRev.Update(func(n int) int { return n + 1 })
			case "h":
				c.Connect()
			}
		}}
	foot := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 4, Padding: core.Symmetric(8, 12), FlexShrink: 0}, Children: []core.Node{
		ui.IconButton{Icon: nswidgets.IconOf("settings"), Label: tr("Settings"), Size: 30, IconSize: 16, OnPress: nav.settings},
		core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), AlignItems: core.AlignEnd}, Children: nav.footer},
	}}
	return core.View{Style: ui.FillRest.Merge(core.Style{Gap: 2}), Children: []core.Node{list, foot}}
}

// projectDialog adds a project: its name and working folder. open shows it
// for a new project.
func projectDialog(cx *core.Scope, c Host, expanded *core.Signal[string]) (node core.Node, open func()) {
	colors := nswidgets.ColorsOf(cx)
	shown := core.NewSignal(cx, false)
	name, path, errText := core.NewSignal(cx, ""), core.NewSignal(cx, ""), core.NewSignal(cx, "")
	confirm := func() {
		n, p := strings.TrimSpace(name.GetUntracked()), strings.TrimSpace(path.GetUntracked())
		if n == "" {
			errText.Set(tr("Enter a project name."))
			return
		}
		if err := c.ChangeWorkspace(p); err != nil {
			errText.Set(err.Error())
			return
		}
		cwd := c.State().Cwd
		expanded.Set(cwd)
		c.RenameProject(cwd, n)
		errText.Set("")
		shown.Set(false)
	}
	open = func() {
		name.Set("")
		path.Set(c.State().Cwd)
		errText.Set("")
		shown.Set(true)
	}
	node = ui.Dialog{Open: shown.Get, OnOpenChange: func(o bool, _ base.Reason) { shown.Set(o) }, TestID: "project-dialog",
		Title: tr("Select project"), Description: tr("Add a project name and choose its working folder."),
		Children: []core.Node{
			ui.Input{Label: tr("Project name"), Placeholder: tr("My project"), Value: name.Get, OnChange: name.Set},
			ui.Input{Label: tr("Project folder"), Placeholder: "/path/to/project", Value: path.Get, OnChange: path.Set},
			core.Show{When: func() bool { return errText.Get() != "" }, Then: func(cx *core.Scope) core.Node {
				return core.Text{TestID: "project-error", ContentFunc: errText.Get, Selectable: core.SelectableOn,
					StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.Danger()} }}
			}},
		},
		Footer: []core.Node{
			ui.Button{Text: tr("Cancel"), Variant: ui.ButtonSecondary, TestID: "project-cancel", OnPress: func() { shown.Set(false) }},
			ui.Button{Text: tr("Select project"), TestID: "project-confirm", OnPress: confirm},
		}}
	return node, open
}
