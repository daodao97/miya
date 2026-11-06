package main

import (
	"strconv"

	"miya/internal/model"
	"miya/internal/remote"

	"github.com/egoist/mygo/ui"
)

// threadMenu offers conversations and files from the composer's project.
func (m *mobileApp) threadMenu(c *ui.Context, project string) {
	end := len(m.draft)
	if m.inlineInput.doc != nil && m.inlineInput.raw == m.draft {
		_, caret := m.inlineInput.doc.Selection()
		end = len(inlineThreadRaw(m.inlineInput.doc.Text().Slice(0, caret)))
	}
	token := composerTokenAt(m.draft, end)
	if token.Kind == '#' {
		m.clearFileReferences()
		if m.state.Collaboration == nil || m.state.Collaboration.Version < 1 {
			return
		}
		allowed := m.delegationAgents()
		p := m.pal
		ui.Column(c.Key("agent-refs")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
			ui.Text(c, tr("Agents")).FontSize(m.font(12)).TextColor(p.muted).Padding(8)
			for _, choice := range agentChoices(token.Query, allowed) {
				row := m.phoneButton(c.Key(choice.Agent)).FillWidth().MinHeight(44).Padding(0, 10).Label("#" + choice.Name)
				row.Children(func() { ui.Text(c, "#"+choice.Name).FontSize(m.font(14)).TextColor(p.ink) })
				if row.Clicked() {
					m.draft = insertAgentReference(m.draft, token, choice.Agent, choice.Name)
				}
			}
		})
		return
	}
	if token.Kind != '@' || project == "" {
		m.clearFileReferences()
		return
	}
	var agents []composerChoice
	if m.state.Collaboration != nil && m.state.Collaboration.Version >= 1 {
		agents = agentChoices(token.Query, m.delegationAgents())
	}
	var shown []threadReference
	if m.state.ReferenceSource != "" {
		shown = matchingThreadRefs(m.state.Projects, m.open, token.Query)
	}
	files := m.searchFileReferences(project, token.Query)
	p := m.pal
	ui.Column(c.Key("thread-refs")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
		ui.Scroll(c.Key("thread-ref-list")).FillWidth().MaxHeight(260).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				if len(agents) > 0 {
					ui.Text(c, tr("Agents")).FontSize(m.font(12)).TextColor(p.muted).Padding(8)
				}
				for _, choice := range agents {
					item := m.phoneButton(c.Key("agent:"+choice.Agent)).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label("@" + choice.Name)
					item.Children(func() {
						agentIcon(c, choice.Agent, 20)
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, "@"+choice.Name).FontSize(m.font(15)).FontWeight(500).SingleLine().Ellipsis("…")
							ui.Text(c, choice.Detail).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
						})
					})
					if item.Clicked() {
						m.draft = insertAgentReference(m.draft, token, choice.Agent, choice.Name)
					}
				}
				if len(shown) > 0 {
					ui.Text(c, tr("Referenced conversation")).FontSize(m.font(12)).TextColor(p.muted).Padding(8)
				}
				for _, ref := range shown {
					t := ref.Thread
					title := threadRefTitle(t)
					detail := backendOf(t).Name() + " · " + ref.ProjectName
					if t.Busy {
						detail += tr(" · Running")
					}
					item := m.phoneButton(c.Key("ref:"+strconv.Itoa(t.ID))).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label(title)
					item.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, title).FontSize(m.font(15)).FontWeight(500).SingleLine().Ellipsis("…")
							ui.Text(c, detail).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
						})
					})
					if item.Clicked() {
						ref := ThreadRef{Source: m.state.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
						m.draft = insertThreadReference(m.draft, token, ref, title)
						if m.conn != nil {
							m.sendPhoneAction(remote.Request{Op: "prefetch", Reference: ref.URL()})
						}
					}
				}
				ui.Text(c, tr("Reference file")).FontSize(m.font(12)).TextColor(p.muted).Padding(8)
				switch {
				case files.loading:
					ui.Text(c, tr("Searching project files…")).FontSize(m.font(13)).TextColor(p.muted).Padding(10)
				case files.err != "":
					ui.Text(c, files.err).FontSize(m.font(13)).TextColor(p.muted).Padding(10)
					if m.state.FileReferences && m.connected && m.phoneButton(c.Key("retry-files")).Label(tr("Retry file search")).Children(func() {
						ui.Text(c, tr("Retry")).FontSize(m.font(13))
					}).Clicked() {
						m.clearFileReferences()
					}
				case len(files.choices) == 0:
					ui.Text(c, tr("No matching files")).FontSize(m.font(13)).TextColor(p.muted).Padding(10)
				}
				for _, choice := range files.choices {
					item := m.phoneButton(c.Key("file:"+choice.Name)).FillWidth().MinHeight(44).Padding(0, 10).Gap(8).Radius(12).Label(choice.Name)
					item.Children(func() {
						icon(c, "file", 18, p.muted)
						ui.Text(c, choice.Name).FontSize(m.font(14)).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…")
					})
					if item.Clicked() {
						m.draft = insertFileReference(m.draft, token, choice.Name)
					}
				}
			})
		})
	})
}

// delegationAgents lists the agents the desktop offers for delegation.
func (m *mobileApp) delegationAgents() []string {
	allowed := []string{}
	for _, agent := range m.state.Agents {
		allowed = append(allowed, agent.Agent)
	}
	return allowed
}
