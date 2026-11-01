package main

import "github.com/egoist/mygo/ui"

func (m *mobileApp) homeBackground() ui.Color {
	if m.dark {
		return ui.Hex("#0c0c0d")
	}
	return ui.Hex("#f6f6f4")
}

func (m *mobileApp) homeAccent() ui.Color {
	if m.dark {
		return ui.Hex("#f2f2f2")
	}
	return ui.Hex("#1a1a1a")
}

// Navigation actions keep a full touch target without a filled button disc.
func (m *mobileApp) homeAction(c *ui.Context, key, label, glyph string) ui.Element {
	b := m.phoneButton(c.Key(key)).Size(44, 44).Padding(0).Radius(12).Background(ui.Transparent).Justify(ui.Center).Label(label)
	if b.Pressed() {
		b.Background(m.pal.surface)
	}
	b.Children(func() { icon(c, glyph, 23, m.homeAccent()) })
	return b
}

func (m *mobileApp) conversationActions(c *ui.Context, button ui.Element, project, agent string) {
	if button.Clicked() {
		c.Blur()
		m.conversationMenu = true
		m.conversationMenuProject, m.conversationMenuAgent = project, agent
	}
}

// startConversation selects a workspace and returns to the same new-conversation
// page used at launch, just as the desktop's New thread action does.
func (m *mobileApp) startConversation(project, agent string) {
	if project == "" {
		project = m.selectedProject().Path
	}
	m.home()
	m.menu = false
	m.drawerSearch, m.drawerQuery = false, ""
	m.selected, m.newAgent, m.title = project, agent, ""
	if project != "" {
		m.expandedProject, m.projectsExpanded = project, true
	}
}

func (m *mobileApp) drawerProject(c *ui.Context, project phoneProject) {
	p := m.pal
	expanded := m.expandedProject == project.Path
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			row := m.phoneButton(c.Key("open")).Grow(1).MinWidth(0).MinHeight(44).Padding(0).Gap(10).Radius(10).Label("项目 " + project.Name)
			if row.Pressed() {
				row.Background(p.surface)
			}
			row.Children(func() {
				icon(c, "folder", 18, p.muted)
				ui.Text(c, project.Name).FontSize(m.font(15)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				busy := false
				for _, thread := range project.Threads {
					busy = busy || thread.Busy
				}
				if busy && !expanded {
					ui.Spinner(c).FontSize(m.font(12)).Label("项目进行中：" + project.Name)
				} else {
					glyph := "chevron-right"
					if expanded {
						glyph = "chevron-down"
					}
					icon(c, glyph, 12, p.muted)
				}
			})
			if row.Clicked() {
				m.showProject(project.Path)
			}
		})
		if !expanded {
			return
		}
		ui.Column(c.Key("threads")).FillWidth().Children(func() {
			if len(project.Threads) == 0 {
				ui.Text(c, "还没有对话").Padding(8, 0).FontSize(m.font(14)).TextColor(p.muted)
			}
			more := m.threadListExpanded[project.Path]
			for _, thread := range displayedThreads(project.Threads, more) {
				m.drawerThread(c.Key(thread.ID), thread)
			}
			if len(project.Threads) > collapsedThreadCount {
				label := "展开显示"
				if more {
					label = "收起"
				}
				button := m.phoneButton(c.Key("more-threads")).FillWidth().MinHeight(44).Padding(0).Radius(10).Label(label)
				button.Children(func() { ui.Text(c, label).FontSize(m.font(15)).TextColor(p.muted) })
				if button.Clicked() {
					if m.threadListExpanded == nil {
						m.threadListExpanded = map[string]bool{}
					}
					m.threadListExpanded[project.Path] = !more
				}
			}
		})
	})
}

func (m *mobileApp) drawerThread(c *ui.Context, thread Thread) {
	p := m.pal
	title := firstNonEmpty(thread.Title, "未命名对话")
	row := m.phoneButton(c).FillWidth().MinHeight(48).Padding(0).Gap(10).Radius(10).Background(ui.Transparent).Label(nativeText(title))
	if thread.ID == m.open || row.Pressed() {
		row.Background(p.surface)
	}
	row.Children(func() {
		agentIcon(c, threadAgent(thread), 18)
		ui.Text(c, nativeText(title)).FontSize(m.font(16)).FontWeight(400).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		m.threadStatus(c, thread)
	})
	if row.Clicked() {
		m.openThread(thread)
	}
}
