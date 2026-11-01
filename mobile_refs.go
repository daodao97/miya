package main

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

// threadMenu lists all projects' other conversations above the composer
// while the caret follows an @ and the beginning of a conversation's title
// or #id. Choosing one references it as @#<id>, which the desktop expands
// into its context as the message is sent.
func (m *mobileApp) threadMenu(c *ui.Context, project string) {
	end := len(m.draft)
	if m.inlineInput.doc != nil && m.inlineInput.raw == m.draft {
		_, caret := m.inlineInput.doc.Selection()
		end = len(inlineThreadRaw(m.inlineInput.doc.Text().Slice(0, caret)))
	}
	token := composerTokenAt(m.draft, end)
	if token.Kind != '@' || project == "" {
		return
	}
	shown := matchingThreadRefs(m.state.Projects, m.open, token.Query)
	p := m.pal
	ui.Column(c.Key("thread-refs")).FillWidth().Background(p.surface).Radius(18).Padding(6).Children(func() {
		if len(shown) == 0 {
			ui.Text(c, "没有匹配的对话").FontSize(m.font(13)).TextColor(p.muted).Padding(10)
			return
		}
		ui.Text(c, "引用对话").FontSize(m.font(12)).TextColor(p.muted).Padding(8)
		ui.Scroll(c.Key("thread-ref-list")).FillWidth().MaxHeight(260).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
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
							ui.Text(c, "#"+strconv.Itoa(t.ID)+" "+title).FontSize(m.font(15)).FontWeight(500).SingleLine().Ellipsis("…")
							ui.Text(c, detail).FontSize(m.font(12)).TextColor(p.muted).SingleLine().Ellipsis("…")
						})
					})
					if item.Clicked() {
						m.draft = insertThreadReference(m.draft, token, t.ID)
					}
				}
			})
		})
	})
}
