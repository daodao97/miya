package nsmobile

import (
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/interaction"
	mcpaccess "miya/internal/mcp/access"
	"miya/internal/nsrequests"
	phoneproto "miya/internal/phone/protocol"
)

func (m *App) DescribeApproval(a interaction.Request) appmodel.ApprovalView {
	var threads []model.Thread
	for _, p := range m.State.Projects {
		threads = append(threads, p.Threads...)
	}
	return appmodel.DescribeApproval(threads, a)
}
func (m *App) Answer(a interaction.Request, r interaction.Response) {
	m.SubmitApproval(a, r, false)
	m.change()
}
func (m *App) RespondMCPAlways(a interaction.Request) error {
	if mcpaccess.Rememberable(a) {
		m.SubmitApproval(a, interaction.Response{Action: interaction.Allow}, true)
		m.change()
	}
	return nil
}
func (m *App) RequestPending(a interaction.Request) bool {
	m.revision.Get()
	return !m.Connected || m.Client.RequestPending(a)
}
func (m *App) RequestError(a interaction.Request) string {
	m.revision.Get()
	if !m.Connected {
		return tr("Not connected to desktop")
	}
	return m.Client.RequestError(a)
}
func (m *App) requestCards(cx *core.Scope) core.Node {
	return core.Scroll{Style: core.Style{MaxHeight: core.Pt(180)}, Children: []core.Node{core.Each(live(m, m.ConversationApprovals), func(a interaction.Request) string { return string(a.ID) }, func(cx *core.Scope, get func() interaction.Request, _ func() int) core.Node {
		id := core.Untracked(cx, func() string { return string(get().ID) })
		return nsrequests.TouchButton("review-"+id, func() string { a := get(); return m.DescribeApproval(a).Title + " · " + a.AgentName() }, nil, func() { m.pages.Open("request:" + id) })
	})}}
}
func (m *App) requestSheet(cx *core.Scope, id string) core.Node {
	key := core.NewMemo(cx, live(m, func() string {
		for _, a := range m.State.Approvals {
			if string(a.ID) == id {
				return phoneproto.ApprovalFingerprint(a)
			}
		}
		return ""
	}))
	return m.groupedPage(cx, core.Dynamic(func(cx *core.Scope) core.Node {
		version := key.Get()
		if version == "" {
			return groupedState(cx, "check", func() string { return tr("This request has been resolved") }, nil, nil)
		}
		for _, a := range m.State.Approvals {
			if string(a.ID) == id {
				return nsrequests.Card(cx, m, a)
			}
		}
		return core.View{}
	}))
}

func (m *App) RequestFields(a interaction.Request) map[string]string {
	if m.requestFields == nil {
		m.requestFields = map[string]map[string]string{}
	}
	key := m.requestFieldKey(a)
	if m.requestFields[key] == nil {
		m.requestFields[key] = map[string]string{}
	}
	return m.requestFields[key]
}

func (m *App) requestFieldKey(a interaction.Request) string {
	return m.Link + ":" + string(a.ID) + ":" + phoneproto.ApprovalFingerprint(a)
}
