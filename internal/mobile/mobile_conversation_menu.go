package mobile

import (
	widgets "miya/internal/widgets"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func (m *App) conversationActionsSheet(c *ui.Context) {
	target := float32(0)
	if m.ConversationMenu {
		target = 1
	}
	shown := c.Root().AnimateTo("conversation-actions", target, ui.Animation{Duration: 220 * time.Millisecond, Ease: ui.EaseOut})
	visible := m.ConversationMenu || shown > 0.001
	p, safe := m.Pal, c.SafeArea()
	height := max(float32(0), c.Root().Bounds().H-safe.Top-safe.Bottom-20)
	ui.DialogBase(c.Key("conversation-actions"), &visible, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35*shown)).AlignItems(ui.Center).Justify(ui.End).
			Padding(safe.Top+12, safe.Right+12, safe.Bottom+8, safe.Left+12)
		panel.Column().FillWidth().MaxWidth(520).MaxHeight(height).MinHeight(0).Gap(8).Translate(0, c.Root().Bounds().H*(1-shown)).Disabled(!m.ConversationMenu).Label(tr("Conversation actions"))
		// Keep Cancel visible while long actions scroll within the safe area.
		ui.Scroll(c.Key("actions-list")).FillWidth().MinHeight(0).Shrink(1).Radius(22).Clip().Background(p.surface).Children(func() {
			ui.Column(c).FillWidth().Padding(8).Children(func() {
				ui.Text(c, tr("Conversation actions")).FillWidth().TextAlign(ui.Center).FontSize(m.Font(13)).TextColor(p.muted).Padding(12, 8)
				files := m.conversationAction(c.Key("files"), tr("Project files"), "folder").Disabled(!m.Connected || m.conversationMenuProject == "")
				if files.Clicked() {
					m.ConversationMenu = false
					m.openProjectFiles(m.conversationMenuProject)
				}
				if m.conversationAction(c.Key("web"), tr("Web services"), "globe").Disabled(!m.Connected).Clicked() {
					m.ConversationMenu = false
					m.OpenWebServices(m.conversationMenuProject)
				}
				if m.Open != 0 {
					if m.conversationAction(c.Key("desktop-control"), tr("Remote desktop"), "device-desktop").Disabled(!m.Connected || m.DesktopRequest != "").Clicked() {
						m.ConversationMenu = false
						m.openRemoteDesktop()
					}
					if m.conversationAction(c.Key("runtime"), tr("Background tasks"), "terminal-2").Disabled(!m.Connected).Clicked() {
						m.ConversationMenu = false
						m.OpenWebServices(m.conversationMenuProject)
						m.WebDetails = true
					}
				}
				ui.Column(c).FillWidth().Padding(0, 16).Children(func() {
					ui.Box(c).FillWidth().Height(1).Background(p.line)
				})
				newThread := m.conversationAction(c.Key("new"), tr("New conversation"), "edit").Disabled(m.conversationMenuProject == "")
				if newThread.Clicked() {
					m.ConversationMenu = false
					m.StartConversation(m.conversationMenuProject, m.conversationMenuAgent)
				}
			})
		})
		cancel := m.phoneButton(c.Key("cancel")).FillWidth().MinHeight(56).Shrink(0).Padding(14).Radius(18).Background(p.surface).Justify(ui.Center).Label(tr("Cancel"))
		cancel.Children(func() { ui.Text(c, tr("Cancel")).FontSize(m.Font(17)).FontWeight(600) })
		if cancel.Clicked() {
			m.ConversationMenu = false
		}
	})
	if !visible {
		m.ConversationMenu = false
	}
}

func (m *App) conversationAction(c *ui.Context, label, glyph string) ui.Element {
	button := m.phoneButton(c).FillWidth().MinHeight(58).Shrink(0).Padding(14, 16).Gap(14).Radius(14).Label(label)
	if button.Pressed() {
		button.Background(m.Pal.line)
	}
	button.Children(func() {
		widgets.Icon(c, glyph, 22, m.Pal.ink)
		ui.Text(c, label).Grow(1).MinWidth(0).FontSize(m.Font(17))
	})
	return button
}
