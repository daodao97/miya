package mobile

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
	"miya/internal/remote"
	"miya/internal/widgets"
)

// threadMenu offers conversations and files from the composer's project.
func (m *App) threadMenu(c *ui.Context, project string) {
	end := len(m.Draft)
	if m.InlineInput.Doc != nil && m.InlineInput.Raw == m.Draft {
		_, caret := m.InlineInput.Doc.Selection()
		end = len(conversation.InlineThreadRaw(m.InlineInput.Doc.Text().Slice(0, caret)))
	}
	token := composer.ComposerTokenAt(m.Draft, end)
	if token.Kind == '#' {
		m.clearFileReferences()
		if m.State.Collaboration == nil || m.State.Collaboration.Version < 1 {
			return
		}
		allowed := m.delegationAgents()
		p := m.Pal
		ui.Column(c.Key("agent-refs")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
			ui.Text(c, tr("Agents")).FontSize(m.Font(12)).TextColor(p.muted).Padding(8)
			for _, choice := range composer.AgentChoices(token.Query, allowed) {
				row := m.phoneButton(c.Key(choice.Agent)).FillWidth().MinHeight(44).Padding(0, 10).Label("#" + choice.Name)
				row.Children(func() { ui.Text(c, "#"+choice.Name).FontSize(m.Font(14)).TextColor(p.ink) })
				if row.Clicked() {
					m.Draft = composer.InsertAgentReference(m.Draft, token, choice.Agent, choice.Name)
				}
			}
		})
		return
	}
	if token.Kind != '@' || project == "" {
		m.clearFileReferences()
		return
	}
	var agents []model.ComposerChoice
	if m.State.Collaboration != nil && m.State.Collaboration.Version >= 1 {
		agents = composer.AgentChoices(token.Query, m.delegationAgents())
	}
	var shown []appmodel.ThreadReference
	if m.State.ReferenceSource != "" {
		shown = appmodel.MatchingThreadRefs(m.allPhoneProjects(), m.Open, token.Query)
	}
	files := m.SearchFileReferences(project, token.Query)
	p := m.Pal
	ui.Column(c.Key("thread-refs")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
		ui.Scroll(c.Key("thread-ref-list")).FillWidth().MaxHeight(260).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				if len(agents) > 0 {
					ui.Text(c, tr("Agents")).FontSize(m.Font(12)).TextColor(p.muted).Padding(8)
				}
				for _, choice := range agents {
					item := m.phoneButton(c.Key("agent:"+choice.Agent)).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label("@" + choice.Name)
					item.Children(func() {
						widgets.AgentIcon(c, choice.Agent, 20)
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, "@"+choice.Name).FontSize(m.Font(15)).FontWeight(500).SingleLine().Ellipsis("…")
							ui.Text(c, choice.Detail).FontSize(m.Font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
						})
					})
					if item.Clicked() {
						m.Draft = composer.InsertAgentReference(m.Draft, token, choice.Agent, choice.Name)
					}
				}
				if len(shown) > 0 {
					ui.Text(c, tr("Referenced conversation")).FontSize(m.Font(12)).TextColor(p.muted).Padding(8)
				}
				for _, ref := range shown {
					t := ref.Thread
					title := appmodel.ThreadRefTitle(t)
					detail := appmodel.AgentName(model.ThreadAgent(t)) + " · " + ref.ProjectName
					if t.Busy {
						detail += tr(" · Running")
					}
					item := m.phoneButton(c.Key("ref:"+strconv.Itoa(t.ID))).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label(title)
					item.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, title).FontSize(m.Font(15)).FontWeight(500).SingleLine().Ellipsis("…")
							ui.Text(c, detail).FontSize(m.Font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
						})
					})
					if item.Clicked() {
						ref := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
						m.Draft = composer.InsertThreadReference(m.Draft, token, ref, title)
						if m.Conn != nil {
							m.sendPhoneAction(remote.Request{Op: "prefetch", Reference: ref.URL()})
						}
					}
				}
				ui.Text(c, tr("Reference file")).FontSize(m.Font(12)).TextColor(p.muted).Padding(8)
				switch {
				case files.Loading:
					ui.Text(c, tr("Searching project files…")).FontSize(m.Font(13)).TextColor(p.muted).Padding(10)
				case files.err != "":
					ui.Text(c, files.err).FontSize(m.Font(13)).TextColor(p.muted).Padding(10)
					if m.State.FileReferences && m.Connected && m.phoneButton(c.Key("retry-files")).Label(tr("Retry file search")).Children(func() {
						ui.Text(c, tr("Retry")).FontSize(m.Font(13))
					}).Clicked() {
						m.clearFileReferences()
					}
				case len(files.Choices) == 0:
					ui.Text(c, tr("No matching files")).FontSize(m.Font(13)).TextColor(p.muted).Padding(10)
				}
				for _, choice := range files.Choices {
					item := m.phoneButton(c.Key("file:"+choice.Name)).FillWidth().MinHeight(44).Padding(0, 10).Gap(8).Radius(12).Label(choice.Name)
					item.Children(func() {
						widgets.Icon(c, "file", 18, p.muted)
						ui.Text(c, choice.Name).FontSize(m.Font(14)).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…")
					})
					if item.Clicked() {
						m.Draft = composer.InsertFileReference(m.Draft, token, choice.Name)
					}
				}
			})
		})
	})
}

// delegationAgents lists the agents the desktop offers for delegation.
func (m *App) delegationAgents() []string {
	allowed := []string{}
	for _, agent := range m.State.Agents {
		allowed = append(allowed, agent.Agent)
	}
	return allowed
}
