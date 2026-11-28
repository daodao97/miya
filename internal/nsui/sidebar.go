package nsui

import (
	"fmt"
	"path/filepath"
	"slices"
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
	cwd, history                string
	projects                    []projectmodel.Project
	threads                     []model.Thread
	waiting, running, completed []model.Thread
	busy                        map[string]bool
	unread                      map[int]bool
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
	d.waiting, d.running, d.completed = msgfmt.SidebarActivityThreads(snapshots, c.WaitingThreads().Has, c.DesktopThreadUnread)
	for _, list := range [][]model.Thread{d.threads, d.waiting, d.running, d.completed} {
		for _, t := range list {
			if !t.Busy && c.DesktopThreadUnread(t) {
				d.unread[t.ID] = true
			}
		}
	}
	return d
}

func (d sidebarData) find(id int) (model.Thread, bool) {
	for _, list := range [][]model.Thread{d.threads, d.waiting, d.running, d.completed} {
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
// settings and the workspace at the foot.
func sidebar(cx *core.Scope, c Host, nav shellNav) core.Node {
	more, automationRuns := map[string]bool{}, map[string]bool{}
	moreRev := core.NewSignal(cx, 0)
	data := core.NewMemoFunc(cx, Live(func() sidebarData { return gatherSidebar(c) }), nil)
	current := core.NewMemo(cx, Live(func() int { return c.Current().ID }))
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
	threadItem := func(prefix, testPrefix string, t model.Thread, unread, nested bool) sidebarItem {
		id := strconv.Itoa(t.ID)
		return sidebarItem{id: prefix + id, testID: testPrefix + id, text: threadTitle(t),
			agent: model.ThreadAgent(t), automation: t.Automation != "", nested: nested,
			loading: t.Loading, busy: t.Busy, unread: unread}
	}
	secondaryItem := func(id, testID, text string, nested bool, indent core.Points) sidebarItem {
		return sidebarItem{id: id, testID: testID, text: text, nested: nested, indent: indent}
	}
	// threadItems are the open project's conversations, newest first and
	// compact unless expanded, with the history's state when it has none.
	threadItems := func(d sidebarData, path string, nested bool) []sidebarItem {
		var items []sidebarItem
		if len(d.threads) == 0 && d.history != "" && (d.history == tr("Loading conversations…") ||
			strings.HasPrefix(d.history, "Unable to load") || strings.HasPrefix(d.history, "History")) {
			items = append(items, secondaryItem("h:"+path, "history-status", d.history, nested, threadTitleIndent))
		}
		threads, runs := msgfmt.SplitAutomationThreads(d.threads, func(t model.Thread) bool {
			return t.Busy || d.unread[t.ID] || t.ID == current.Get()
		})
		for _, t := range msgfmt.DisplayedThreads(threads, more[path]) {
			items = append(items, threadItem("t:", "thread-", t, d.unread[t.ID], nested))
		}
		if len(threads) > msgfmt.CollapsedThreadCount {
			label := tr("Expand")
			if more[path] {
				label = tr("Collapse")
			}
			items = append(items, secondaryItem("m:"+path, "more-threads", label, nested, 0))
		}
		if len(runs) > 0 {
			row := secondaryItem("r:"+path, "automation-runs-"+path, fmt.Sprintf(tr("Automation runs (%d)"), len(runs)), nested, threadTitleIndent)
			row.expanded = automationRuns[path]
			items = append(items, row)
			if automationRuns[path] {
				for _, t := range runs {
					items = append(items, threadItem("t:", "thread-", t, false, nested))
				}
			}
		}
		return items
	}
	// The activity sections fold from their titles. Their state outlives
	// a section that empties and comes back.
	activityOpen := map[string]*core.Signal[bool]{"waiting": core.NewSignal(cx, true), "running": core.NewSignal(cx, true), "completed": core.NewSignal(cx, true)}
	sections := core.NewMemoFunc(cx, func() []sidebarSection {
		moreRev.Get()
		d, expanded := data.Get(), nav.expanded.Get()
		sections := []sidebarSection{{id: "navigation", items: []sidebarItem{
			{id: "nav:new", text: tr("New thread"), icon: "edit", testID: "new-thread"},
			{id: "nav:automations", text: tr("Automations"), icon: "clock", testID: "open-automations"},
			{id: "nav:bots", text: tr("Bots"), icon: "message-circle", testID: "open-bots"},
		}}}
		for _, a := range []struct {
			key, title string
			threads    []model.Thread
		}{{"waiting", tr("Waiting for you"), d.waiting}, {"running", tr("In progress"), d.running}, {"completed", tr("Completed"), d.completed}} {
			if len(a.threads) > 0 {
				items := make([]sidebarItem, 0, len(a.threads))
				for _, t := range a.threads {
					items = append(items, threadItem("a:", "activity-", t, d.unread[t.ID], false))
				}
				sections = append(sections, sidebarSection{id: "activity-" + a.key, title: a.title, items: items, open: activityOpen[a.key]})
			}
		}
		cwdProject := false
		var projects []sidebarItem
		for _, p := range d.projects {
			open := p.Path == expanded
			projects = append(projects, sidebarItem{id: "p:" + p.Path, text: p.Name, testID: "project-" + p.Path,
				icon: "folder", expanded: open, busy: !open && d.busy[p.Path]})
			if p.Path == d.cwd {
				cwdProject = true
				if open {
					projects = append(projects, threadItems(d, p.Path, true)...)
				}
			}
		}
		if !cwdProject {
			if items := threadItems(d, d.cwd, false); len(items) > 0 {
				sections = append(sections, sidebarSection{id: "threads-open", title: tr("Conversations"), items: items})
			}
		}
		return append(sections, sidebarSection{id: "projects", title: tr("Projects"), items: projects})
	}, func(a, b []sidebarSection) bool {
		return slices.EqualFunc(a, b, func(a, b sidebarSection) bool {
			return a.id == b.id && a.title == b.title && a.open == b.open && slices.Equal(a.items, b.items)
		})
	}).Get
	list := sidebarList{sections: sections, addProject: nav.project,
		selected: func() string {
			if page := nav.page.Get(); page == "automations" || page == "bots" {
				return "nav:" + page
			}
			return "t:" + strconv.Itoa(current.Get())
		},
		menu: func(item sidebarItem) (menu []core.Node, newThread func()) {
			kind, rest, _ := strings.Cut(item.id, ":")
			switch kind {
			case "p":
				return []core.Node{
					ui.MenuItem{Text: tr("Archive project"), OnSelect: func() { c.ArchiveProject(rest) }},
					ui.MenuSeparator{},
					ui.MenuItem{Text: tr("Delete project"), Variant: ui.MenuItemDanger, OnSelect: func() { c.DeleteProject(rest) }},
				}, func() {
					for _, p := range data.GetUntracked().projects {
						if p.Path == rest {
							newThreadIn(p)
							return
						}
					}
				}
			case "a", "t":
				id, _ := strconv.Atoi(rest)
				act := func(action func(model.Thread)) func() {
					return func() {
						if t, ok := data.GetUntracked().find(id); ok {
							action(t)
						}
					}
				}
				return []core.Node{
					ui.MenuItem{Text: tr("Archive conversation"), OnSelect: act(c.ArchiveThread)},
					ui.MenuItem{Text: tr("Delete conversation"), Variant: ui.MenuItemDanger, OnSelect: act(c.DeleteThread)},
				}, nil
			}
			return nil, nil
		},
		choose: func(id string) {
			kind, rest, _ := strings.Cut(id, ":")
			switch kind {
			case "nav":
				switch rest {
				case "new":
					c.NewThread()
				case "automations", "bots":
					nav.page.Set(rest)
				}
			case "t", "a":
				if n, err := strconv.Atoi(rest); err == nil {
					if t, ok := data.GetUntracked().find(n); ok {
						c.SelectThread(t)
					}
				}
			case "p":
				for _, p := range data.GetUntracked().projects {
					if p.Path == rest {
						selectProject(p)
					}
				}
			case "m":
				more[rest] = !more[rest]
				moreRev.Update(func(n int) int { return n + 1 })
			case "r":
				automationRuns[rest] = !automationRuns[rest]
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

// projectDialog edits or adds a project by name and working folder.
func projectDialog(cx *core.Scope, c Host, expanded *core.Signal[string]) (node core.Node, open func(projectName string)) {
	colors := nswidgets.ColorsOf(cx)
	shown := core.NewSignal(cx, false)
	name, path, errText := core.NewSignal(cx, ""), core.NewSignal(cx, ""), core.NewSignal(cx, "")
	pickerOpen := core.NewSignal(cx, false)
	picker, browse := folderPicker(cx, c, pickerOpen, func(p string) {
		path.Set(p)
		if strings.TrimSpace(name.GetUntracked()) == "" {
			name.Set(filepath.Base(p))
		}
	})
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
	submit := func(string) { confirm() }
	open = func(projectName string) {
		name.Set(projectName)
		path.Set(c.State().Cwd)
		errText.Set("")
		shown.Set(true)
	}
	dialog := ui.Dialog{Open: shown.Get, OnOpenChange: func(o bool, _ base.Reason) { shown.Set(o) }, TestID: "project-dialog",
		Title: tr("Select project"), Description: tr("Add a project name and choose its working folder."),
		Children: []core.Node{
			ui.Input{Label: tr("Project name"), Placeholder: tr("My project"), Value: name.Get, OnChange: name.Set, AutoFocus: true, OnSubmit: submit},
			core.View{Style: core.Style{Direction: core.Row, Gap: 8, AlignItems: core.AlignEnd}, Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					ui.Input{Label: tr("Project folder"), Placeholder: "/path/to/project", Value: path.Get, OnChange: path.Set, OnSubmit: submit},
				}},
				ui.Button{Text: tr("Browse…"), Variant: ui.ButtonSecondary, TestID: "project-browse", OnPress: func() { browse(path.GetUntracked()) }},
			}},
			core.Show{When: func() bool { return errText.Get() != "" }, Then: func(cx *core.Scope) core.Node {
				return core.Text{TestID: "project-error", ContentFunc: errText.Get, Selectable: core.SelectableOn,
					StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.Danger()} }}
			}},
		},
		Footer: []core.Node{
			ui.Button{Text: tr("Cancel"), Variant: ui.ButtonSecondary, TestID: "project-cancel", OnPress: func() { shown.Set(false) }},
			ui.Button{Text: tr("Select project"), TestID: "project-confirm", OnPress: confirm},
		}}
	return core.Group([]core.Node{dialog, picker}), open
}
