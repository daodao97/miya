package main

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

// threadMenu offers conversations and files from the composer's project.
func (m *mobileApp) threadMenu(c *ui.Context, project string) {
	end := len(m.draft)
	if m.inlineInput.doc != nil && m.inlineInput.raw == m.draft {
		_, caret := m.inlineInput.doc.Selection()
		end = len(inlineThreadRaw(m.inlineInput.doc.Text().Slice(0, caret)))
	}
	token := composerTokenAt(m.draft, end)
	if token.Kind != '@' || project == "" {
		m.clearFileReferences()
		return
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
				if len(shown) > 0 {
					ui.Text(c, "引用对话").FontSize(m.font(12)).TextColor(p.muted).Padding(8)
				}
				for _, ref := range shown {
					t := ref.Thread
					title := threadRefTitle(t)
					detail := backendOf(t).Name() + " · " + ref.ProjectName
					if t.Busy {
						detail += " · 运行中"
					}
					item := m.phoneButton(c.Key("ref:"+strconv.Itoa(t.ID))).FillWidth().MinHeight(52).Padding(0, 10).Radius(12).Label(title)
					item.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, title).FontSize(m.font(15)).FontWeight(500).SingleLine().Ellipsis("…")
							ui.Text(c, detail).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
						})
					})
					if item.Clicked() {
						ref := ThreadRef{Source: m.state.ReferenceSource, Backend: threadAgent(t), SessionID: t.ServerID}
						m.draft = insertThreadReference(m.draft, token, ref, title)
						if m.conn != nil {
							m.sendPhoneAction(remote.Request{Op: "prefetch", Reference: ref.URL()})
						}
					}
				}
				ui.Text(c, "引用文件").FontSize(m.font(12)).TextColor(p.muted).Padding(8)
				switch {
				case files.loading:
					ui.Text(c, "正在搜索项目文件…").FontSize(m.font(13)).TextColor(p.muted).Padding(10)
				case files.err != "":
					ui.Text(c, files.err).FontSize(m.font(13)).TextColor(p.muted).Padding(10)
					if m.state.FileReferences && m.connected && m.phoneButton(c.Key("retry-files")).Label("重试搜索文件").Children(func() {
						ui.Text(c, "重试").FontSize(m.font(13))
					}).Clicked() {
						m.clearFileReferences()
					}
				case len(files.choices) == 0:
					ui.Text(c, "没有匹配的文件").FontSize(m.font(13)).TextColor(p.muted).Padding(10)
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
