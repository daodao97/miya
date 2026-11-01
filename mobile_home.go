package main

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"
)

type mobileHomeThread struct {
	thread   Thread
	project  phoneProject
	priority int
}

func (m *mobileApp) homeBackground() ui.Color {
	if m.dark {
		return ui.Hex("#000000")
	}
	return ui.Hex("#f2f2f7")
}

func (m *mobileApp) homeSurface() ui.Color {
	if m.dark {
		return ui.Hex("#1c1c1e")
	}
	return ui.Hex("#ffffff")
}

func (m *mobileApp) homeAccent() ui.Color {
	if m.dark {
		return ui.Hex("#0a84ff")
	}
	return ui.Hex("#007aff")
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

// homeThreads gives attention to conversations needing action before recency.
// Sorting a separate slice leaves the desktop's project order untouched.
func (m *mobileApp) homeThreads() []mobileHomeThread {
	var rows []mobileHomeThread
	for _, project := range m.state.Projects {
		if m.selected != "" && project.Path != m.selected {
			continue
		}
		for _, thread := range project.Threads {
			priority := 0
			if len(m.approvalsFor(thread.ServerID)) > 0 {
				priority = 3
			} else if thread.Busy {
				priority = 2
			} else if _, unread := m.unreadFor(thread); unread {
				priority = 1
			}
			rows = append(rows, mobileHomeThread{thread, project, priority})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].priority != rows[j].priority {
			return rows[i].priority > rows[j].priority
		}
		return rows[i].thread.LastActivityAt > rows[j].thread.LastActivityAt
	})
	return rows
}

// startConversation makes composing explicit and leaves the list below it
// in the navigation stack, so the back button and edge gesture return there.
func (m *mobileApp) startConversation(project, agent string) {
	if project == "" {
		project = m.selectedProject().Path
	}
	if project == "" {
		return
	}
	m.home()
	m.menu = false
	m.newProject, m.newAgent, m.title = project, agent, ""
	m.router().Push("/thread")
}

func (m *mobileApp) conversationHome(c *ui.Context) {
	p := m.pal
	rows := m.homeThreads()
	if len(rows) == 0 {
		ui.Column(c).Grow(1).MinHeight(0).Padding(28).Gap(12).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			icon(c, "message-circle", 36, p.muted)
			ui.Text(c, "还没有会话").FontSize(m.font(20)).FontWeight(600)
			ui.Text(c, "新建一个会话，开始处理项目中的任务。").FontSize(m.font(14)).TextColor(p.muted).TextAlign(ui.Center)
		})
		return
	}
	ui.Scroll(c.Key("home-conversations")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(0, 20, 24, 20).Children(func() {
			for start := 0; start < len(rows); {
				end := start + 1
				for end < len(rows) && rows[end].priority == rows[start].priority {
					end++
				}
				group := rows[start:end]
				label := []string{"最近会话", "未读", "运行中", "待审批"}[group[0].priority]
				ui.Text(c, label).FontSize(m.font(13)).TextColor(p.muted).Margin(20, 16, 7, 16)
				ui.Column(c.Key(group[0].priority)).FillWidth().Radius(14).Clip().Background(m.homeSurface()).Children(func() {
					for i, row := range group {
						m.homeThread(c.Key(row.project.Path), row)
						if i < len(group)-1 {
							ui.Box(c.Key(fmt.Sprintf("separator:%d:%s", row.thread.ID, row.project.Path))).FillWidth().Height(0.5).Margin(0, 0, 0, 60).Background(p.line)
						}
					}
				})
				start = end
			}
		})
	})
}

func (m *mobileApp) homeThread(c *ui.Context, row mobileHomeThread) {
	p, t := m.pal, row.thread
	title := firstNonEmpty(t.Title, "未命名会话")
	item := m.phoneButton(c.Key(t.ID)).FillWidth().MinHeight(72).Padding(10, 14).Gap(12).Radius(0).Background(ui.Transparent).Label(nativeText(title))
	if item.Pressed() {
		item.Background(p.line)
	}
	item.Children(func() {
		ui.Row(c).Size(32, 32).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			agentIcon(c, threadAgent(t), 25)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, nativeText(title)).FontSize(m.font(16)).FontWeight(600).SingleLine().Ellipsis("…")
			detail := row.project.Name + " · " + agentName(threadAgent(t))
			ui.Text(c, detail).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
		})
		ui.Column(c).Gap(9).AlignItems(ui.End).Children(func() {
			if t.Age != "" {
				ui.Text(c, t.Age).FontSize(m.font(11)).TextColor(p.muted).SingleLine()
			}
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				switch row.priority {
				case 3:
					icon(c, "alert-circle", 13, p.warn)
				case 2:
					ui.Spinner(c).FontSize(m.font(12)).TextColor(m.homeAccent())
				case 1:
					ui.Box(c).Size(7, 7).Radius(4).Background(m.homeAccent())
				}
				icon(c, "chevron-right", 12, p.muted)
			})
		})
	})
	if item.Clicked() {
		m.openThread(t)
	}
}
