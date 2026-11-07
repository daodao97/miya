package mobile

import (
	"time"

	"github.com/egoist/mygo/ui"
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
		ui.Scroll(c.Key("actions-list")).FillWidth().MinHeight(0).Shrink(1).Radius(14).Clip().Background(m.settingsSurface()).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				ui.Text(c, tr("Conversation actions")).FillWidth().Wrap().TextAlign(ui.Center).FontSize(m.Font(13)).TextColor(p.muted).Padding(16)
				m.conversationActionDivider(c.Key("title-divider"))
				files := m.conversationAction(c.Key("files"), tr("Project files"), !m.Connected || m.conversationMenuProject == "")
				if files.Clicked() {
					m.ConversationMenu = false
					m.openProjectFiles(m.conversationMenuProject)
				}
				m.conversationActionDivider(c.Key("files-divider"))
				if m.conversationAction(c.Key("web"), tr("Web services"), !m.Connected).Clicked() {
					m.ConversationMenu = false
					m.OpenWebServices(m.conversationMenuProject)
				}
				if m.Open != 0 {
					m.conversationActionDivider(c.Key("web-divider"))
					if m.conversationAction(c.Key("desktop-control"), tr("Remote desktop"), !m.Connected || m.DesktopRequest != "").Clicked() {
						m.ConversationMenu = false
						m.openRemoteDesktop()
					}
					m.conversationActionDivider(c.Key("desktop-divider"))
					if m.conversationAction(c.Key("runtime"), tr("Background tasks"), !m.Connected).Clicked() {
						m.ConversationMenu = false
						m.OpenWebServices(m.conversationMenuProject)
						m.WebDetails = true
					}
				}
				m.conversationActionDivider(c.Key("new-divider"))
				newThread := m.conversationAction(c.Key("new"), tr("New conversation"), m.conversationMenuProject == "")
				if newThread.Clicked() {
					m.ConversationMenu = false
					m.StartConversation(m.conversationMenuProject, m.conversationMenuAgent)
				}
			})
		})
		cancel := m.phoneButton(c.Key("cancel")).FillWidth().MinHeight(58).Shrink(0).Padding(14, 16).Radius(14).Background(m.settingsSurface()).Justify(ui.Center).Label(tr("Cancel"))
		cancel.Children(func() {
			ui.Text(c, tr("Cancel")).FontSize(m.Font(20)).FontWeight(600).TextColor(p.link).MinWidth(0).Wrap().TextAlign(ui.Center)
		})
		if cancel.Clicked() {
			m.ConversationMenu = false
		}
	})
	if !visible {
		m.ConversationMenu = false
	}
}

func (m *App) conversationAction(c *ui.Context, label string, disabled bool) ui.Element {
	button := m.phoneButton(c).FillWidth().MinHeight(58).Shrink(0).Padding(14, 16).Radius(0).Justify(ui.Center).Label(label).Disabled(disabled).Opacity(1)
	color := m.Pal.link
	if disabled {
		color = m.Pal.muted
	} else if button.Pressed() {
		button.Background(m.Pal.line)
	}
	button.Children(func() {
		ui.Text(c, label).Grow(1).MinWidth(0).FontSize(m.Font(20)).TextColor(color).Wrap().TextAlign(ui.Center)
	})
	return button
}

func (m *App) conversationActionDivider(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).Background(m.Pal.line)
}
