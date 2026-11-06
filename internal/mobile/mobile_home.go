package mobile

import (
	"github.com/egoist/mygo/ui"

	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (m *App) HomeBackground() ui.Color {
	if m.Dark {
		return ui.Hex("#0c0c0d")
	}
	return ui.Hex("#f6f6f4")
}

func (m *App) homeAccent() ui.Color {
	if m.Dark {
		return ui.Hex("#f2f2f2")
	}
	return ui.Hex("#1a1a1a")
}

// Navigation actions keep a full touch target without a filled button disc.
func (m *App) homeAction(c *ui.Context, key, label, glyph string) ui.Element {
	b := m.phoneButton(c.Key(key)).Size(44, 44).Padding(0).Radius(12).Background(ui.Transparent).Justify(ui.Center).Label(label)
	if b.Pressed() {
		b.Background(m.Pal.surface)
	}
	b.Children(func() { widgets.Icon(c, glyph, 23, m.homeAccent()) })
	return b
}

func (m *App) conversationActions(c *ui.Context, button ui.Element, project, agent string) {
	if button.Clicked() {
		c.Blur()
		m.ConversationMenu = true
		m.conversationMenuProject, m.conversationMenuAgent = project, agent
	}
}

// startConversation selects a workspace and returns to the same new-conversation
// page used at launch, just as the desktop's New thread action does.
func (m *App) StartConversation(project, agent string) {
	if project == "" {
		project = m.selectedProject().Path
	}
	m.Home()
	m.Menu = false
	m.DrawerSearch, m.DrawerQuery = false, ""
	m.Selected, m.NewAgent, m.Title = project, agent, ""
	if project != "" {
		m.ExpandedProject, m.projectsExpanded = project, true
	}
}

func (m *App) drawerProject(c *ui.Context, project model.ProjectSnapshot) {
	p := m.Pal
	expanded := m.ExpandedProject == project.Path
	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			row := m.phoneButton(c.Key("open")).Grow(1).MinWidth(0).MinHeight(44).Padding(0).Gap(10).Radius(10).Role(ui.RoleDisclosure).Expanded(expanded).Label(tr("Project ") + project.Name)
			if row.Pressed() {
				row.Background(p.surface)
			}
			row.Children(func() {
				widgets.Icon(c, "folder", 18, p.muted)
				ui.Text(c, project.Name).FontSize(m.Font(15)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				busy := false
				for _, thread := range project.Threads {
					busy = busy || thread.Busy
				}
				if busy && !expanded {
					ui.Spinner(c).FontSize(m.Font(12)).Label(tr("Project activity: ") + project.Name)
				} else {
					glyph := "chevron-right"
					if expanded {
						glyph = "chevron-down"
					}
					widgets.Icon(c, glyph, 12, p.muted)
				}
			})
			if row.Clicked() {
				m.ShowProject(project.Path)
			}
		})
		if !expanded {
			return
		}
		ui.Column(c.Key("threads")).FillWidth().Children(func() {
			if len(project.Threads) == 0 {
				ui.Text(c, tr("No conversations yet")).Padding(8, 0).FontSize(m.Font(14)).TextColor(p.muted)
			}
			more := m.ThreadListExpanded[project.Path]
			for _, thread := range msgfmt.DisplayedThreads(project.Threads, more) {
				m.drawerThread(c.Key(thread.ID), thread)
			}
			if len(project.Threads) > msgfmt.CollapsedThreadCount {
				label := tr("Expand")
				if more {
					label = tr("Collapse")
				}
				button := m.phoneButton(c.Key("more-threads")).FillWidth().MinHeight(44).Padding(0).Radius(10).Role(ui.RoleDisclosure).Expanded(more).Label(label)
				button.Children(func() { ui.Text(c, label).FontSize(m.Font(15)).TextColor(p.muted) })
				if button.Clicked() {
					if m.ThreadListExpanded == nil {
						m.ThreadListExpanded = map[string]bool{}
					}
					m.ThreadListExpanded[project.Path] = !more
				}
			}
		})
	})
}

func (m *App) drawerThread(c *ui.Context, thread model.Thread) {
	p := m.Pal
	title := textutil.FirstNonEmpty(thread.Title, tr("Untitled conversation"))
	row := m.phoneButton(c).FillWidth().MinHeight(48).Padding(0).Gap(10).Radius(10).Background(ui.Transparent).Label(msgfmt.NativeText(title))
	if thread.ID == m.Open || row.Pressed() {
		row.Background(p.surface)
	}
	row.Children(func() {
		widgets.AgentIcon(c, model.ThreadAgent(thread), 18)
		if m.textScale > 1.3 {
			row.Padding(8, 0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, msgfmt.NativeText(title)).FontSize(m.Font(16)).FontWeight(400).MaxLines(3).Ellipsis("…")
				m.threadStatus(c, thread)
			})
		} else {
			ui.Text(c, msgfmt.NativeText(title)).FontSize(m.Font(16)).FontWeight(400).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			m.threadStatus(c, thread)
		}
	})
	if row.Clicked() {
		m.OpenThread(thread)
	}
}
