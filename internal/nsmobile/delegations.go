package nsmobile

import (
	"encoding/json"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/nsrequests"
)

type phoneCollaborationHost struct{ m *App }

func (h phoneCollaborationHost) CollaborationCapabilities() *model.CollaborationStatus {
	h.m.revision.Get()
	return h.m.State.Collaboration
}
func (h phoneCollaborationHost) ReadOnly() bool {
	h.m.revision.Get()
	return !h.m.Connected || h.m.Viewer.Current().ReadOnly
}
func (h phoneCollaborationHost) OpenReference(raw string) {
	h.m.OpenReference(raw)
	if h.m.SendErr == "" {
		h.m.pages.Open("")
	}
	h.m.change()
}
func (m *App) delegationsSheet(cx *core.Scope) core.Node {
	if m.State.Collaboration == nil || m.State.Collaboration.Version < 1 || m.Open == 0 {
		return scroll(text(tr("Update the Miya background service to use Agent tasks")), m.sheetBack("delegations-close", tr("Done"), ""))
	}
	if m.collaborationOwner != m.Link {
		m.collaborations = nsrequests.CollaborationUI{}
		m.collaborationOwner = m.Link
	}
	t := m.Viewer.Current()
	from := (model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}).URL()
	p := m.collaborations.Pane(from)
	call := nsrequests.CollaborationCall(func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		m.CallCollaboration(r, func(raw json.RawMessage, err error) { done(raw, err); m.change() })
	})
	m.pollSheet(cx, func() { p.Refresh(m.State.Collaboration, call, false) })
	p.LoadedAt = time.Time{}
	return scroll(text(tr("Agent tasks")), m.sheetBack("delegations-close", tr("Done"), ""), nsrequests.CollaborationContent(cx, phoneCollaborationHost{m}, p, call, m.change, func() { m.revision.Get() }))
}
